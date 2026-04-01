package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"github.com/golang-jwt/jwt/v5"
	"go.alis.build/alog"
	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const DefaultAgentTarget = "localhost:8085"
const HistoryExtensionURI = "https://a2a.alis.build/extensions/history/v1"

type response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Config struct {
	AgentTarget string
}

type Option func(*Config)

func WithAgentTarget(target string) Option {
	return func(cfg *Config) {
		cfg.AgentTarget = target
	}
}

func newConfig(opts ...Option) *Config {
	cfg := &Config{
		AgentTarget: DefaultAgentTarget,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

func normalizeAgentTarget(target string) string {
	if target == "" {
		return DefaultAgentTarget
	}
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://")
}

func callAgent(ctx context.Context, target, prompt, userID, email, token string) error {
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
		return err
	}

	ctx = a2aclient.AttachServiceParams(ctx, a2aclient.ServiceParams{
		"x-alis-forwarded-authorization": {"Bearer " + token},
		"x-alis-user-id":                 {userID},
		"x-alis-user-email":              {email},
		a2a.SvcParamExtensions:           {HistoryExtensionURI},
	})

	_, err = client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(
			a2a.MessageRoleUser,
			a2a.NewTextPart(prompt),
		),
	})
	if err != nil {
		return err
	}
	alog.Info(ctx, "Agent invocation completed")

	return nil
}

func handleError(ctx context.Context, w http.ResponseWriter, msg string) {
	alog.Errorf(ctx, "error: %s", msg)
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Status: "FAILED", Error: msg})
}

func NewCronHandler(service pb.SchedulerServiceServer, opts ...Option) http.HandlerFunc {
	cfg := newConfig(opts...)

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

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
		ownerID := strings.Split(cron.GetOwner(), "/")[1]

		// Prepare a 'x-alis-forwarded-authorization' header
		jwt := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":   ownerID,
			"email": cron.GetEmail(),
		})
		token, err := jwt.SignedString([]byte("authz-key")) // Internally trusted
		if err != nil {
			handleError(ctx, w, err.Error())
			return
		}

		// Invoke agent
		newCtx := context.WithoutCancel(ctx)
		go func() {
			err = callAgent(newCtx, cfg.AgentTarget, cron.GetPrompt(), ownerID, cron.GetEmail(), token)
			if err != nil {
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
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response{Status: "OK"}); err != nil {
			alog.Errorf(ctx, "failed to encode response: %v", err)
		}
	}
}
