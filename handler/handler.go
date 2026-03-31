package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"context"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"go.alis.build/alog"
	"github.com/golang-jwt/jwt/v5"

	pb "go.alis.build/common/alis/a2a/extension/scheduler/v1"
)

const (
	// Endpoint for handling scheduled invocations
	SchedulerExtensionHandlerPath = "/alis.a2a.extension.v1.SchedulerService/handler"
)

type response struct {
	Status string `json:"status"`
	Error string  `json:"error,omitempty"`
}

type bearerAuthTransport struct {
	XAlisForwardToken  string
	Email  string
	UserID string
	rt     http.RoundTripper
}

func (b *bearerAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("x-alis-forwarded-authorization", "Bearer "+b.XAlisForwardToken)
	cloned.Header.Set("x-alis-user-id", b.UserID)
	cloned.Header.Set("x-alis-user-email", b.Email)
	rt := b.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(cloned)
}

func callAgent(ctx context.Context, target, prompt, userID, email, token string) error {

	endpoints := []*a2a.AgentInterface{
		{
			URL: 			 target,
			ProtocolBinding: a2a.TransportProtocolJSONRPC,
			ProtocolVersion: "1.0.0",
		},
	}

	httpClient := &http.Client{
		Transport: &bearerAuthTransport{
			XAlisForwardToken: token,
			UserID: userID,
			Email:  email,
			rt:     http.DefaultTransport,
		},
	}

	client, err := a2aclient.NewFromEndpoints(ctx, endpoints, a2aclient.WithJSONRPCTransport(httpClient))
	if err != nil {
		return err
	}

	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(
			a2a.MessageRoleUser, 
			a2a.NewTextPart(prompt),
		),
	})
	if err != nil {
		return err
	}
	_, err = json.Marshal(resp)
	if err != nil {
		return err
	}

	return nil
}

func handleError(ctx context.Context, w http.ResponseWriter, msg string) {
    alog.Errorf(ctx, "error: %s", msg)
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Status: "FAILED", Error: msg})
}

func NewCronHandler(target string, service pb.SchedulerServiceServer) http.HandlerFunc {
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
		go func (){
			err = callAgent(newCtx, target, cron.GetPrompt(), ownerID, cron.GetEmail(), token)
			if err != nil {
				alog.Errorf(ctx, "agent invocation failed: %v", err)
			}
		} ()
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response{Status: "OK"}); err != nil {
			alog.Errorf(ctx, "failed to encode response: %v", err)
		}
	}
}