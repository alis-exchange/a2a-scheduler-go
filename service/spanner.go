package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/spanner"
	"github.com/google/uuid"
	"github.com/mennanov/fmutils"
	"go.alis.build/iam/v3"
	authz "go.alis.build/iam/v3/authz"
	"go.alis.build/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	taskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"

	cloudscheduler "cloud.google.com/go/scheduler/apiv1"
	schedulerpb "cloud.google.com/go/scheduler/apiv1/schedulerpb"
)

const (
	cronRegex      = `^crons/[a-z0-9-]{2,50}$`
	roleOpen       = "roles/open"
	roleCronOwner  = "roles/cron.owner"
	cronsTableName = "Crons"
)

// SchedulerServiceConfig selects the Spanner database and logical table naming used by [SchedulerService].
type SchedulerServiceConfig struct {
	SpannerProject    string // GCP project id for cloud spanner database resources
	SchedulingProject string // GCP project id for scheduling resources
	SchedulingQueue   string // Cloud Tasks Queue for scheduling crons
	SchedulingRegion  string // Region in which scheduling infrastructure is allocated
	Instance          string // Spanner instance id
	ServiceAccount    string // Name of service account responsible for invoking scheduled crons.
	Audience          string // Target audience for OIDC auth token generation
	Database          string // Spanner database id
	DatabaseRole      string // optional Spanner database role for fine-grained access (empty if unused)
	TablePrefix       string // optional prefix applied to the logical Crons table name
	TargetUrl         string // Target URL for triggering crons.
}

var _ pb.SchedulerServiceServer = (*SchedulerService)(nil)

// SchedulerService is an implementation of [pb.SchedulerServiceServer] for managing Crons via Google Cloud Spanner.
type SchedulerService struct {
	db             *spanner.Client
	cloudTasks     *cloudtasks.Client
	cloudScheduler *cloudscheduler.CloudSchedulerClient
	config         *SchedulerServiceConfig
	pb.UnimplementedSchedulerServiceServer
}

func init() {
	// We register these roles globally
	authz.AddOpenRolePermissions(roleOpen, []string{
		pb.SchedulerService_CreateCron_FullMethodName,
		pb.SchedulerService_ListCrons_FullMethodName,
	})
	authz.AddRolePermissions(roleCronOwner, []string{
		pb.SchedulerService_GetCron_FullMethodName,
		pb.SchedulerService_UpdateCron_FullMethodName,
		pb.SchedulerService_DeleteCron_FullMethodName,
		pb.SchedulerService_RunCron_FullMethodName,
	})
}

// NewSchedulerService constructs a [SchedulerService] with a Spanner client and IAM authorizer wired to
// the SchedulerService RPC names used by this module.
func NewSchedulerService(ctx context.Context, config *SchedulerServiceConfig) (*SchedulerService, error) {
	dbName := fmt.Sprintf("projects/%s/instances/%s/databases/%s", config.SpannerProject, config.Instance, config.Database)

	db, err := spanner.NewClientWithConfig(ctx, dbName, spanner.ClientConfig{
		DisableNativeMetrics: true,
		DatabaseRole:         config.DatabaseRole,
	})
	if err != nil {
		return nil, err
	}

	cloudTasks, err := cloudtasks.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	cloudScheduler, err := cloudscheduler.NewCloudSchedulerClient(ctx)
	if err != nil {
		return nil, err
	}

	return &SchedulerService{
		db:             db,
		cloudScheduler: cloudScheduler,
		cloudTasks:     cloudTasks,
		config:         config,
	}, nil
}

func (s *SchedulerService) cronsTable() string {
	if s.config.TablePrefix == "" {
		return cronsTableName
	}
	return s.config.TablePrefix + "_" + cronsTableName
}

// CreateCron implements the [Service.CreateCron] method.
func (s *SchedulerService) CreateCron(ctx context.Context, req *pb.CreateCronRequest) (*pb.Cron, error) {
	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)
	if !az.HasPermission(pb.SchedulerService_CreateCron_FullMethodName) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}

	// Validation
	validator := validation.NewValidator()
	validator.MessageIsPopulated("cron", req.GetCron() != nil)
	validator.String("cron.prompt", req.GetCron().GetPrompt()).IsPopulated()
	validator.Enum("cron.type", req.GetCron().GetType()).IsOneof(pb.Cron_TYPE_CRON, pb.Cron_TYPE_AT)
	validator.If(validator.Enum("cron.type", req.GetCron().GetType()).Is(pb.Cron_TYPE_CRON)).Then(
		validator.String("cron.timezone", req.GetCron().GetTimezone()).IsPopulated(),
		validator.String("expr", req.GetCron().GetExpr()).IsPopulated(),
	)
	validator.If(validator.Enum("cron.type", req.GetCron().GetType()).Is(pb.Cron_TYPE_AT)).Then(
		validator.Timestamp("cron.at", req.GetCron().GetAt()).IsPopulated(),
	)
	validator.If(validator.Enum("cron.type", req.GetCron().GetType()).Is(pb.Cron_TYPE_AT)).Then(
		validator.String("cron.initial_prompt", req.GetCron().GetInitialPrompt()).IsEmpty(),
	)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Create unique ID for this Cron
	cronID := uuid.NewString()

	type InvocationRequest struct {
		CronID string `json:"id"` // Cron job ID reference
	}

	body := InvocationRequest{CronID: cronID}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	switch req.GetCron().GetType() {
	case pb.Cron_TYPE_CRON:
		parent := fmt.Sprintf("projects/%s/locations/%s", s.config.SchedulingProject, s.config.SchedulingRegion)
		jobReq := &schedulerpb.CreateJobRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", s.config.SchedulingProject, s.config.SchedulingRegion),
			Job: &schedulerpb.Job{
				Name: fmt.Sprintf("%s/jobs/%s", parent, cronID),
				Target: &schedulerpb.Job_HttpTarget{
					HttpTarget: &schedulerpb.HttpTarget{
						Uri:        s.config.TargetUrl,
						HttpMethod: schedulerpb.HttpMethod_POST,
						Body:       bodyBytes,
						AuthorizationHeader: &schedulerpb.HttpTarget_OidcToken{
							OidcToken: &schedulerpb.OidcToken{
								ServiceAccountEmail: s.config.ServiceAccount,
								Audience:            s.config.Audience,
							},
						},
					},
				},
				Schedule: req.GetCron().GetExpr(),
				TimeZone: req.GetCron().GetTimezone(),
				RetryConfig: &schedulerpb.RetryConfig{
					RetryCount: 1,
				},
			},
		}
		if _, err = s.cloudScheduler.CreateJob(ctx, jobReq); err != nil {
			return nil, err
		}
	case pb.Cron_TYPE_AT:
		queueName := fmt.Sprintf("projects/%s/locations/%s/queues/%s", s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue)
		taskName := fmt.Sprintf("%s/tasks/%s", queueName, cronID)
		taskReq := &taskspb.CreateTaskRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s/queues/%s", s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue),
			Task: &taskspb.Task{
				Name: taskName,
				MessageType: &taskspb.Task_HttpRequest{
					HttpRequest: &taskspb.HttpRequest{
						Url:        s.config.TargetUrl,
						HttpMethod: taskspb.HttpMethod_POST,
						Body:       bodyBytes,
						AuthorizationHeader: &taskspb.HttpRequest_OidcToken{
							OidcToken: &taskspb.OidcToken{
								ServiceAccountEmail: s.config.ServiceAccount,
								Audience:            s.config.Audience,
							},
						},
					},
				},
				ScheduleTime: req.GetCron().GetAt(),
			},
		}
		if _, err = s.cloudTasks.CreateTask(ctx, taskReq); err != nil {
			return nil, err
		}
	}

	// Set the name, create and update time
	req.GetCron().Name = "crons/" + cronID
	now := timestamppb.Now()
	req.GetCron().CreateTime = now
	req.GetCron().UpdateTime = now
	req.GetCron().State = pb.Cron_STATE_ACTIVE
	req.GetCron().ArchiveTime = nil
	req.GetCron().LastRunTime = nil

	// Set owner and email from authorizer details
	req.GetCron().Owner = "users/" + caller.ID
	req.GetCron().Email = caller.Email

	// Insert new resource
	var mutations []*spanner.Mutation
	policy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    roleCronOwner,
				Members: []string{caller.PolicyMember()},
			},
		},
	}
	mutation := spanner.Insert(s.cronsTable(), []string{"key", "Cron", "Policy"}, []any{req.GetCron().GetName(), req.GetCron(), policy})
	mutations = append(mutations, mutation)

	// Apply mutations in a single transaction
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := txn.BufferWrite(mutations); err != nil {
			return nil
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return req.GetCron(), nil
}

// UpdateCron implements the [Service.UpdateCron] method.
func (s *SchedulerService) UpdateCron(ctx context.Context, req *pb.UpdateCronRequest) (*pb.Cron, error) {
	// Validation
	validator := validation.NewValidator()
	validator.MessageIsPopulated("cron", req.GetCron() != nil)
	validator.MessageIsPopulated("update_mask", req.GetUpdateMask() != nil)
	validator.StringList("update_mask.paths", req.GetUpdateMask().GetPaths()).IsPopulated()
	validator.String("name", req.Cron.GetName()).IsPopulated().Matches(cronRegex)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)
	cron, policy, err := s.readCron(ctx, req.Cron.GetName())
	if err != nil {
		return nil, err
	}
	if !az.HasPermission(pb.SchedulerService_UpdateCron_FullMethodName, policy) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}

	// Apply update
	clonedUpdatedMsg := proto.Clone(req.GetCron())
	fmutils.Filter(clonedUpdatedMsg, req.UpdateMask.GetPaths())
	fmutils.Prune(cron, req.UpdateMask.GetPaths())
	proto.Merge(cron, clonedUpdatedMsg)

	// Update the update time
	cron.UpdateTime = timestamppb.Now()

	// Update db
	mutation := spanner.Update(s.cronsTable(), []string{"key", "Cron"}, []any{req.GetCron().GetName(), cron})
	if _, err = s.db.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return nil, err
	}
	return cron, nil
}

// GetCron implements the [Service.GetCron] method.
func (s *SchedulerService) GetCron(ctx context.Context, req *pb.GetCronRequest) (*pb.Cron, error) {
	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)

	// Validation
	validator := validation.NewValidator()
	validator.String("name", req.GetName()).IsPopulated().Matches(cronRegex)
	if err := validator.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Read the resource from the database
	cron, policy, err := s.readCron(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	// Check if the requester has access to this resource
	if !az.HasPermission(pb.SchedulerService_GetCron_FullMethodName, policy) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}
	return cron, nil
}

// ListCrons implements the [Service.ListCrons] method.
func (s *SchedulerService) ListCrons(ctx context.Context, req *pb.ListCronsRequest) (*pb.ListCronsResponse, error) {
	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)
	if !az.HasPermission(pb.SchedulerService_ListCrons_FullMethodName) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}

	// Prepare query statement
	statement := spanner.NewStatement(`select Cron from ` + s.cronsTable() + " as t")
	if !caller.IsSystem() {
		statement.SQL += `
			WHERE EXISTS (
			SELECT 1
			FROM UNNEST(t.Policy.bindings) AS binding
			CROSS JOIN UNNEST(binding.members) AS member
			WHERE member = @member
			)`
		statement.Params["member"] = caller.PolicyMember()
	}
	statement.SQL += ` order by t.create_time DESC limit @limit offset @offset;`

	// set query parameters
	limit := int(req.GetPageSize())
	if limit < 1 || limit > 100 {
		limit = 100
	}
	statement.Params["limit"] = limit
	offset := 0
	var err error
	if req.GetPageToken() != "" {
		offset, err = strconv.Atoi(req.GetPageToken())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token")
		}
	}
	statement.Params["offset"] = offset

	// make db hit and build up results
	var resources []*pb.Cron
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		cron := &pb.Cron{}
		if err := r.Columns(cron); err != nil {
			return err
		}
		resources = append(resources, cron)
		return nil
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "querying database: %v", err)
	}

	// determine next page token
	nextPageToken := ""
	if len(resources) < limit {
		nextPageToken = fmt.Sprintf("%d", offset+limit)
	}

	return &pb.ListCronsResponse{
		Crons:         resources,
		NextPageToken: nextPageToken,
	}, nil
}

// DeleteCron implements the [Service.DeleteCron] method.
func (s *SchedulerService) DeleteCron(ctx context.Context, req *pb.DeleteCronRequest) (*emptypb.Empty, error) {
	// Validation
	validator := validation.NewValidator()
	validator.String("name", req.GetName()).IsPopulated().Matches(cronRegex)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)
	cron, policy, err := s.readCron(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	if !az.HasPermission(pb.SchedulerService_DeleteCron_FullMethodName, policy) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}

	// Delete scheduler instances
	cronID := strings.Split(req.GetName(), "/")[1]
	switch cron.GetType() {
	case pb.Cron_TYPE_CRON:
		if err = s.cloudScheduler.DeleteJob(ctx, &schedulerpb.DeleteJobRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/jobs/%s",
				s.config.SchedulingProject, s.config.SchedulingRegion, cronID),
		}); err != nil {
			return nil, err
		}
	case pb.Cron_TYPE_AT:
		if err = s.cloudTasks.DeleteTask(ctx, &taskspb.DeleteTaskRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s",
				s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue, cronID),
		}); err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
		}
	}

	m := spanner.Delete(s.cronsTable(), spanner.Key{req.GetName()})
	if _, err = s.db.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// RunCron implements the [Service.RunCron] method.
func (s *SchedulerService) RunCron(ctx context.Context, req *pb.RunCronRequest) (*pb.RunCronResponse, error) {
	// Validation
	validator := validation.NewValidator()
	validator.String("id", req.GetId()).IsPopulated()
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	caller := iam.MustFromContext(ctx)
	az := authz.MustNew(caller)

	cron, policy, err := s.readCron(ctx, fmt.Sprintf("crons/%s", req.GetId()))
	if err != nil {
		return nil, err
	}
	if !az.HasPermission(pb.SchedulerService_RunCron_FullMethodName, policy) {
		return nil, status.Error(codes.PermissionDenied, "permission denied")
	}
	if cron.GetState() == pb.Cron_STATE_ARCHIVED {
		return nil, status.Error(codes.FailedPrecondition, "archived cron cannot be run")
	}

	switch cron.GetType() {
	case pb.Cron_TYPE_CRON:
		if _, err := s.cloudScheduler.RunJob(ctx, &schedulerpb.RunJobRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/jobs/%s",
				s.config.SchedulingProject, s.config.SchedulingRegion, req.GetId()),
		}); err != nil {
			return nil, err
		}
	case pb.Cron_TYPE_AT:
		if _, err := s.cloudTasks.RunTask(ctx, &taskspb.RunTaskRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s",
				s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue, req.GetId()),
		}); err != nil {
			return nil, err
		}
	}
	return &pb.RunCronResponse{}, nil
}

// readCron loads the Cron and Policy columns for a cron primary key, or returns the Spanner error
// (typically NotFound if the row does not exist).
func (s *SchedulerService) readCron(ctx context.Context, name string) (*pb.Cron, *iampb.Policy, error) {
	row, err := s.db.Single().ReadRow(ctx, s.cronsTable(), spanner.Key{name}, []string{"Cron", "Policy"})
	if err != nil {
		return nil, nil, err
	}
	cron := &pb.Cron{}
	policy := &iampb.Policy{}

	if err := row.Columns(cron, policy); err != nil {
		return nil, nil, err
	}
	return cron, policy, nil
}
