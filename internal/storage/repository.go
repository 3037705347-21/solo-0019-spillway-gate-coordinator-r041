package storage

import (
	"context"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

type Repository interface {
	CreateGate(ctx context.Context, gate domain.Gate) error
	Gate(ctx context.Context, id string) (domain.Gate, error)
	CreateCommand(ctx context.Context, command domain.GateCommand) error
	Command(ctx context.Context, id string) (domain.GateCommand, error)
	ReviewCommand(
		ctx context.Context,
		commandID string,
		decision domain.InterlockDecision,
		token string,
		now time.Time,
	) (domain.GateCommand, domain.Gate, error)
	ExecuteCommand(
		ctx context.Context,
		commandID string,
		token string,
		operator string,
		simulateFault bool,
		now time.Time,
	) (domain.GateCommand, domain.Gate, error)
	CreatePlan(ctx context.Context, plan domain.OperationPlan) error
	Plan(ctx context.Context, id string) (domain.OperationPlan, error)
	AdvancePlan(
		ctx context.Context,
		planID string,
		sequence int,
		commandID string,
		decision domain.InterlockDecision,
		token string,
		now time.Time,
	) (domain.OperationPlan, domain.GateCommand, domain.Gate, error)
	ExecutePlanStage(
		ctx context.Context,
		planID string,
		sequence int,
		token string,
		operator string,
		simulateFault bool,
		now time.Time,
	) (domain.OperationPlan, domain.GateCommand, domain.Gate, error)
}
