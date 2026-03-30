package service

import (
	"context"

	v1 "go.alis.build/common/alis/a2a/extension/scheduler/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Service is the persistence contract for A2A Crons.
type Service interface {
	CreateCron(ctx context.Context, req *v1.CreateCronRequest) (*v1.Cron, error)
	GetCron(ctx context.Context, req *v1.GetCronRequest) (*v1.Cron, error)
	UpdateCron(ctx context.Context, req *v1.UpdateCronRequest) (*v1.Cron, error)
	DeleteCron(ctx context.Context, req *v1.DeleteCronRequest) (*emptypb.Empty, error)
	ListCrons(ctx context.Context, req *v1.ListCronsRequest) (*v1.ListCronsResponse, error)
	RunCron(ctx context.Context, req *v1.RunCronRequest) (*v1.RunCronResponse, error)
}
