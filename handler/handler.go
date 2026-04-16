package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"go.alis.build/alog"
	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
	"go.alis.build/iam/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DefaultAgentTarget is the fallback A2A agent endpoint used by the handler.
const DefaultAgentTarget = "localhost:8085"

// HistoryExtensionURI enables the history extension on forwarded A2A requests.
const HistoryExtensionURI = "https://a2a.alis.build/extensions/history/v1"

// response is the JSON payload returned by the HTTP handler.
type response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Config configures how the cron handler connects to the downstream agent.
type Config struct {
	AgentTarget string
	Identity    *iam.Identity
}

// Option mutates a Config during handler construction.
type Option func(*Config)

// WithAgentTarget overrides the downstream A2A agent target used by the handler.
func WithAgentTarget(target string) Option {
	return func(cfg *Config) {
		cfg.AgentTarget = target
	}
}

// WithAuthenticatedIdentity overrides the identity the cron handler uses for
// local scheduler service calls.
func WithAuthenticatedIdentity(id, email string) Option {
	return func(cfg *Config) {
		cfg.Identity = newIdentity(id, email)
	}
}

// WithAuthenticatedServiceAccount sets the authenticated service identity from a service account email.
func WithAuthenticatedServiceAccount(email string) Option {
	return WithAuthenticatedIdentity(email, email)
}

func defaultAuthenticatedIdentity() *iam.Identity {
	projectID := os.Getenv("ALIS_OS_PROJECT")
	if projectID == "" {
		return nil
	}
	email := fmt.Sprintf("alis-build@%s.iam.gserviceaccount.com", projectID)
	return newIdentity(email, email)
}

// newConfig builds a Config from the provided options and defaults.
func newConfig(opts ...Option) *Config {
	cfg := &Config{
		AgentTarget: DefaultAgentTarget,
		Identity:    defaultAuthenticatedIdentity(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// normalizeAgentTarget strips any scheme and returns a host:port target.
func normalizeAgentTarget(target string) string {
	if target == "" {
		return DefaultAgentTarget
	}
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://")
}

// callAgent forwards the cron prompt to the configured A2A agent as the owner.
func callAgent(ctx context.Context, target, prompt, contextID string, userID, email string) (string, error) {
	endpoints := []*a2a.AgentInterface{
		{
			URL:             normalizeAgentTarget(target),
			ProtocolBinding: a2a.TransportProtocolGRPC,
			ProtocolVersion: "1.0.0",
		},
	}

	client, err := a2aclient.NewFromEndpoints(
		ctx,
		endpoints,
		a2agrpc.WithGRPCTransport(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		return "", err
	}

	caller := newIdentity(userID, email)
	// ctx = caller.OutgoingMetadata(ctx)
	ctx = caller.Context(ctx)
	serviceParams := a2aclient.ServiceParams{
		a2a.SvcParamExtensions: {HistoryExtensionURI},
	}
	ctx = a2aclient.AttachServiceParams(ctx, serviceParams)

	message := a2a.NewMessage(
		a2a.MessageRoleUser,
		a2a.NewTextPart(prompt),
	)
	// Reuse the cron's existing A2A context so repeated executions stay in one thread.
	if contextID != "" {
		message.ContextID = contextID
	}

	result, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: message,
	})
	if err != nil {
		return "", err
	}
	alog.Info(ctx, "Agent invocation completed")

	return contextIDFromResult(result), nil
}

// contextIDFromResult extracts the A2A context identifier from a send result.
func contextIDFromResult(result a2a.SendMessageResult) string {
	// A2A send can resolve to either a task or a direct message response, and both
	// shapes can carry the authoritative context identifier we want to persist.
	switch result := result.(type) {
	case *a2a.Task:
		return result.ContextID
	case *a2a.Message:
		return result.ContextID
	default:
		return ""
	}
}

// handleError logs an internal error and writes a standard failure response.
func handleError(ctx context.Context, w http.ResponseWriter, msg string) {
	alog.Errorf(ctx, "error: %s", msg)
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Status: "FAILED", Error: msg})
}

// mergeContextID prefers a newly returned context ID and otherwise keeps the existing one.
func mergeContextID(existing, returned string) string {
	if returned != "" {
		return returned
	}
	return existing
}

func newIdentity(id, email string) *iam.Identity {
	identity := &iam.Identity{
		ID:    id,
		Email: email,
		Type:  iam.User,
	}
	if strings.HasSuffix(email, ".iam.gserviceaccount.com") {
		identity.Type = iam.ServiceAccount
	}
	return identity
}

func contextWithHTTPRequest(ctx context.Context, req *http.Request) context.Context {
	md := metadata.MD{}
	for k, vs := range req.Header {
		md[strings.ToLower(k)] = append([]string(nil), vs...)
	}
	return metadata.NewIncomingContext(ctx, md)
}

// NewCronHandler returns an HTTP handler that executes a stored cron prompt.
func NewCronHandler(service pb.SchedulerServiceServer, opts ...Option) http.HandlerFunc {
	cfg := newConfig(opts...)

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := contextWithHTTPRequest(r.Context(), r)
		if cfg.Identity == nil {
			handleError(ctx, w, "scheduler identity is required; use handler.WithAuthenticatedIdentity or set ALIS_OS_PROJECT")
			return
		}
		ctx = cfg.Identity.Context(ctx)

		// The request body is expected to contain a single cron-id string parameter.
		var body struct {
			CronID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handleError(ctx, w, "failed to decode request body")
			return
		}

		alog.Infof(ctx, "Executing Cron (%s)", body.CronID)

		// Fetch the Cron details.
		cron, err := service.GetCron(ctx, &pb.GetCronRequest{
			Name: fmt.Sprintf("crons/%s", body.CronID),
		})
		if err != nil {
			handleError(ctx, w, err.Error())
			return
		}
		if cron.GetState() == pb.Cron_STATE_ARCHIVED {
			alog.Infof(ctx, "Skipping archived Cron (%s)", body.CronID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(response{Status: "OK"}); err != nil {
				alog.Errorf(ctx, "failed to encode response: %v", err)
			}
			return
		}
		ownerID := strings.Split(cron.GetOwner(), "/")[1]

		// Invoke agent
		newCtx := context.WithoutCancel(ctx)
		go func() {
			contextID := cron.GetContextId()

			// Seed recurring crons with the initial prompt only once, before the first
			// regular prompt is sent, so later executions reuse the same conversation.
			if cron.GetType() == pb.Cron_TYPE_CRON && contextID == "" && cron.GetInitialPrompt() != "" {
				initialContextID, initialErr := callAgent(
					newCtx,
					cfg.AgentTarget,
					cron.GetInitialPrompt(),
					"",
					ownerID,
					cron.GetEmail(),
				)
				if initialErr != nil {
					callErr := initialErr
					if st, ok := status.FromError(callErr); ok {
						alog.Errorf(
							ctx,
							"initial agent invocation failed target=%s owner=%s code=%s message=%q details=%T %v",
							normalizeAgentTarget(cfg.AgentTarget),
							ownerID,
							st.Code(),
							st.Message(),
							st.Details(),
							st.Details(),
						)
						return
					}
					alog.Errorf(
						ctx,
						"initial agent invocation failed target=%s owner=%s err=%T %v",
						normalizeAgentTarget(cfg.AgentTarget),
						ownerID,
						callErr,
						callErr,
					)
					return
				}
				contextID = mergeContextID(contextID, initialContextID)
			}

			returnedContextID, callErr := callAgent(
				newCtx,
				cfg.AgentTarget,
				cron.GetPrompt(),
				contextID,
				ownerID,
				cron.GetEmail(),
			)
			if callErr != nil {
				err := callErr
				if st, ok := status.FromError(err); ok {
					alog.Errorf(
						ctx,
						"agent invocation failed target=%s owner=%s code=%s message=%q details=%T %v",
						normalizeAgentTarget(cfg.AgentTarget),
						ownerID,
						st.Code(),
						st.Message(),
						st.Details(),
						st.Details(),
					)
					return
				}
				alog.Errorf(
					ctx,
					"agent invocation failed target=%s owner=%s err=%T %v",
					normalizeAgentTarget(cfg.AgentTarget),
					ownerID,
					err,
					err,
				)
				return
			}

			contextID = mergeContextID(contextID, returnedContextID)
			now := timestamppb.Now()

			update := &pb.Cron{
				Name:        cron.GetName(),
				ContextId:   contextID,
				LastRunTime: now,
			}
			updateMaskPaths := []string{"last_run_time"}

			if cron.GetContextId() != contextID && contextID != "" {
				updateMaskPaths = append(updateMaskPaths, "context_id")
			}
			if cron.GetType() == pb.Cron_TYPE_AT {
				update.State = pb.Cron_STATE_ARCHIVED
				update.ArchiveTime = now
				updateMaskPaths = append(updateMaskPaths, "state", "archive_time")
			}

			if _, updateErr := service.UpdateCron(newCtx, &pb.UpdateCronRequest{
				Cron: update,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: updateMaskPaths,
				},
			}); updateErr != nil {
				alog.Errorf(
					ctx,
					"failed to persist cron execution state target=%s cron=%s err=%T %v",
					normalizeAgentTarget(cfg.AgentTarget),
					cron.GetName(),
					updateErr,
					updateErr,
				)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response{Status: "OK"}); err != nil {
			alog.Errorf(ctx, "failed to encode response: %v", err)
		}
	}
}
