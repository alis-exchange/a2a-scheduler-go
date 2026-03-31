package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/spanner"
	"github.com/alis-exchange/go-alis-build/iam/v2"
	"github.com/google/uuid"
	"github.com/mennanov/fmutils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"go.alis.build/validation"
	"go.alis.build/common/alis/a2a/extension/scheduler/v1"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	taskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"

	cloudscheduler "cloud.google.com/go/scheduler/apiv1"
	schedulerpb "cloud.google.com/go/scheduler/apiv1/schedulerpb"
)

const (
	cronRegex        = `^crons/[a-z0-9-]{2,50}$`
	roleOpen         = "roles/open"
	roleCronViewer   = "roles/cron.viewer"
	roleCronAdmin    = "roles/cron.admin"
)

// SpannerServiceConfig selects the Spanner database and table names used by [SpannerService].
type SpannerServiceConfig struct {
	SpannerProject    string // GCP project id for cloud spanner database resources
	SchedulingProject string // GCP project id for scheduling resources
	SchedulingQueue   string // Cloud Tasks Queue for scheduling crons
	SchedulingRegion  string // Region in which scheduling infrastructure is allocated
	Instance          string // Spanner instance id
	ServiceAccount    string // Name of service account responsible for invoking scheduled crons.
	Audience          string // Target audience for OIDC auth token generation
	Database          string // Spanner database id
	DatabaseRole      string // optional Spanner database role for fine-grained access (empty if unused)
	CronTable         string // table storing Cron + IAM Policy proto columns
	TargetUrl         string // Target URL for triggering crongs.
}

var _ Service = (*SpannerService)(nil)

// SpannerService is an implementation of [Service] for managing Crons via Google Cloud Spanner.
type SpannerService struct {
	db             *spanner.Client
	cloudTasks     *cloudtasks.Client
	cloudScheduler *cloudscheduler.CloudSchedulerClient
	authorizer     *iam.IAM
	config         *SpannerServiceConfig
	v1.UnimplementedSchedulerServiceServer
}

// NewSpannerService constructs a [SpannerService] with a Spanner client and IAM authorizer wired to
// the SchedulerService RPC names used by this module.
func NewSpannerService(ctx context.Context, config *SpannerServiceConfig) (*SpannerService, error) {
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

	authorizer, err := iam.New([]*iam.Role{
		{
			Name: roleOpen,
			Permissions: []string{
				v1.SchedulerService_CreateCron_FullMethodName,
				v1.SchedulerService_ListCrons_FullMethodName,
			},
			AllUsers: true,
		},
		{
			Name: roleCronViewer,
			Permissions: []string{
				v1.SchedulerService_GetCron_FullMethodName,
			},
			AllUsers: false,
		},
		{
			Name: roleCronAdmin,
			Permissions: []string{
				v1.SchedulerService_GetCron_FullMethodName,
				v1.SchedulerService_UpdateCron_FullMethodName,
				v1.SchedulerService_DeleteCron_FullMethodName,
				v1.SchedulerService_RunCron_FullMethodName,
			},
			AllUsers: false,
		},
	})
	if err != nil {
		return nil, err
	}

	return &SpannerService{
		db:             db,
		cloudScheduler: cloudScheduler, 
		cloudTasks:     cloudTasks,
		config:         config,
		authorizer:     authorizer,
	}, nil
}

// CreateCron implements the [Service.CreateCron] method.
func (s *SpannerService) CreateCron(ctx context.Context, req *v1.CreateCronRequest) (*v1.Cron, error) {
	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}
	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	// Validation
	validator := validation.NewValidator()
	validator.MessageIsPopulated("cron", req.GetCron() != nil)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Create unique ID for this Cron
	cronID := uuid.NewString()

	type InvocationRequest struct {
		CronID string `json:"id"`  // Cron job ID reference
	}

	body := InvocationRequest{CronID: cronID}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	switch req.GetCron().GetType() {
	case v1.Cron_TYPE_CRON:
		// TODO: Consider also handling using Cloud Tasks over Cloud Scheduler
		parent := fmt.Sprintf("projects/%s/locations/%s", s.config.SchedulingProject, s.config.SchedulingRegion)
		jobReq := &schedulerpb.CreateJobRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", s.config.SchedulingProject, s.config.SchedulingRegion),
			Job: &schedulerpb.Job{
				Name: fmt.Sprintf("%s/jobs/%s", parent, cronID),
				Target: &schedulerpb.Job_HttpTarget{
					HttpTarget: &schedulerpb.HttpTarget{
						Uri: s.config.TargetUrl,
						HttpMethod: schedulerpb.HttpMethod_POST,
						Body: bodyBytes,
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
		if _ ,err = s.cloudScheduler.CreateJob(ctx, jobReq); err != nil {
			return nil, err
		}
	case v1.Cron_TYPE_AT:
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
	now :=  timestamppb.Now()
	req.GetCron().CreateTime = now
	req.GetCron().UpdateTime = now

	// Set owner and email from authorizer details
	req.GetCron().Owner = az.Identity.PolicyMember()
	req.GetCron().Email = az.Identity.Email()

	// Insert new resource
	var mutations []*spanner.Mutation
	policy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    roleCronAdmin,
				Members: []string{az.Identity.PolicyMember()},
			},
		},
	}
	mutation := spanner.Insert(s.config.CronTable, []string{"key", "Cron", "Policy"}, []any{req.GetCron().GetName(), req.GetCron(), policy})
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
func (s *SpannerService) UpdateCron(ctx context.Context, req *v1.UpdateCronRequest) (*v1.Cron, error) {
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
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}
	cron, policy, err := s.readCron(ctx, req.Cron.GetName())
	if err != nil {
		return nil, err
	}
	az.AddPolicy(policy)
	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	// Apply update
	clonedUpdatedMsg := proto.Clone(req.GetCron())
	fmutils.Filter(clonedUpdatedMsg, req.UpdateMask.GetPaths())
	fmutils.Prune(cron, req.UpdateMask.GetPaths())
	proto.Merge(cron, clonedUpdatedMsg)

	// Update the update time
	cron.UpdateTime = timestamppb.Now()

	// Update db
	mutation := spanner.Update(s.config.CronTable, []string{"key", "Cron"}, []any{req.GetCron().GetName(), cron})
	if _, err = s.db.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return nil, err
	}
	return cron, nil
}

// GetCron implements the [Service.GetCron] method.
func (s *SpannerService) GetCron(ctx context.Context, req *v1.GetCronRequest) (*v1.Cron, error) {
	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

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
	az.AddPolicy(policy)
	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}
	return cron, nil
}

// ListCrons implements the [Service.ListCrons] method.
func (s *SpannerService) ListCrons(ctx context.Context, req *v1.ListCronsRequest) (*v1.ListCronsResponse, error) {
	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create authorizer: %s", err.Error())
	}

	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	// Prepare query statement
	statement := spanner.NewStatement(`select Cron from ` + s.config.CronTable + " as t")
	if !az.Identity.IsDeploymentServiceAccount() {
		statement.SQL += `
			WHERE EXISTS (
			SELECT 1
			FROM UNNEST(t.Policy.bindings) AS binding
			CROSS JOIN UNNEST(binding.members) AS member
			WHERE member = @member
			)`
		statement.Params["member"] = az.Identity.PolicyMember()
	}
	statement.SQL += ` order by t.create_time DESC limit @limit offset @offset;`

	// set query parameters
	limit := int(req.GetPageSize())
	if limit < 1 || limit > 100 {
		limit = 100
	}
	statement.Params["limit"] = limit
	offset := 0
	if req.GetPageToken() != "" {
		offset, err = strconv.Atoi(req.GetPageToken())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page token")
		}
	}
	statement.Params["offset"] = offset

	// make db hit and build up results
	var resources []*v1.Cron
	iterator := s.db.ReadOnlyTransaction().Query(ctx, statement)
	if err := iterator.Do(func(r *spanner.Row) error {
		history := &v1.Cron{}
		if err := r.Columns(history); err != nil {
			return err
		}
		resources = append(resources, history)
		return nil
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "querying database: %v", err)
	}

	// determine next page token
	nextPageToken := ""
	if len(resources) < limit {
		nextPageToken = fmt.Sprintf("%d", offset+limit)
	}

	return &v1.ListCronsResponse{
		Crons:       resources,
		NextPageToken: nextPageToken,
	}, nil
}

// DeleteCron implements the [Service.DeleteCron] method.
func (s *SpannerService) DeleteCron(ctx context.Context, req *v1.DeleteCronRequest) (*emptypb.Empty, error) {
	// Validation
	validator := validation.NewValidator()
	validator.String("name", req.GetName()).IsPopulated().Matches(cronRegex)
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}
	cron, policy, err := s.readCron(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	az.AddPolicy(policy)
	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	// Delete scheduler instances
	cronID := strings.Split(req.GetName(), "/")[1]
	switch cron.GetType() {
	case v1.Cron_TYPE_CRON:
		if err = s.cloudScheduler.DeleteJob(ctx, &schedulerpb.DeleteJobRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/jobs/%s", 
				s.config.SchedulingProject, "europe-west1", cronID),
		}); err != nil {
			return nil, err
		}
	case v1.Cron_TYPE_AT:
		if err = s.cloudTasks.DeleteTask(ctx, &taskspb.DeleteTaskRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s", 
				s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue, cronID),
		}); err != nil {
			return nil, err
		}
	}

	m := spanner.Delete(s.config.CronTable, spanner.Key{req.GetName()})
	if _, err = s.db.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

// RunCron implements the [Service.RunCron] method.
func (s *SpannerService) RunCron(ctx context.Context, req *v1.RunCronRequest) (*v1.RunCronResponse, error) {
	// Validation
	validator := validation.NewValidator()
	validator.String("id", req.GetId()).IsPopulated()
	if err := validator.Validate(); err != nil {
		return nil, err
	}

	// Authorize
	az, ctx, err := s.authorizer.NewAuthorizer(ctx)
	if err != nil {
		return nil, err
	}

	cron, policy, err := s.readCron(ctx, fmt.Sprintf("crons/%s", req.GetId()))
	if err != nil {
		return nil, err
	}
	az.AddPolicy(policy)
	if err = az.AuthorizeRpc(); err != nil {
		return nil, err
	}

	switch cron.GetType() {
	case v1.Cron_TYPE_CRON:
		if _, err := s.cloudScheduler.RunJob(ctx, &schedulerpb.RunJobRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/jobs/%s", 
				s.config.SchedulingProject, s.config.SchedulingRegion, req.GetId()),
		}); err != nil {
			return nil, err
		}
	case v1.Cron_TYPE_AT:
		if _, err := s.cloudTasks.RunTask(ctx, &taskspb.RunTaskRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s", 
				s.config.SchedulingProject, s.config.SchedulingRegion, s.config.SchedulingQueue, req.GetId()),
		}); err != nil {
			return nil, err
		}
	}
	return &v1.RunCronResponse{}, nil
}

// readCron loads the Cron and Policy columns for a cron primary key, or returns the Spanner error
// (typically NotFound if the row does not exist).
func (s *SpannerService) readCron(ctx context.Context, name string) (*v1.Cron, *iampb.Policy, error) {
	row, err := s.db.Single().ReadRow(ctx, s.config.CronTable, spanner.Key{name}, []string{"Cron", "Policy"})
	if err != nil {
		return nil, nil, err
	}
	cron := &v1.Cron{}
	policy := &iampb.Policy{}

	if err := row.Columns(cron, policy); err != nil {
		return nil, nil, err
	}
	return cron, policy, nil
}
