package coordination

import (
	"context"
	"sync"
	"testing"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
	"example.com/spillway-gate-coordinator/internal/storage"
)

// TestConcurrentAdvanceHasSingleWinner fires many advance calls for the same
// waiting stage at once. Exactly one may create a command and reserve the
// gate; every other attempt must fail without leaving extra commands.
func TestConcurrentAdvanceHasSingleWinner(t *testing.T) {
	repository := storage.NewMemoryRepository()
	service := NewService(repository)
	ctx := context.Background()

	if _, err := service.RegisterGate(ctx, contracts.RegisterGateRequest{
		ID: "gate-p1", Name: "并发闸门", MaxAperture: 4, InitialAperture: 0,
	}); err != nil {
		t.Fatalf("register gate: %v", err)
	}
	if _, err := service.CreatePlan(ctx, contracts.CreatePlanRequest{
		ID: "plan-concurrent", Name: "并发推进计划", RequestedBy: "dispatcher-lin",
		Stages: []contracts.CreatePlanStageRequest{
			{GateID: "gate-p1", TargetAperture: 2, Reason: "唯一阶段"},
		},
	}); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	const contenders = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, _, _, err := service.AdvancePlan(ctx, "plan-concurrent", 1, contracts.AdvancePlanRequest{
				ConfirmedBy: "safety-wu",
				Note:        "并发确认",
			})
			results[index] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case domain.ErrorCodeOf(err) == "stage_already_armed" || domain.ErrorCodeOf(err) == "gate_not_idle":
			conflicts++
		default:
			t.Fatalf("unexpected advance result: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful advance, got %d (%d conflicts)", successes, conflicts)
	}
	if conflicts != contenders-1 {
		t.Fatalf("expected %d losing attempts, got %d", contenders-1, conflicts)
	}

	plan, err := service.Plan(ctx, "plan-concurrent")
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if plan.Stages[0].Status != domain.StageArmed {
		t.Fatalf("stage status: expected armed, got %s", plan.Stages[0].Status)
	}
	gate, err := service.Gate(ctx, "gate-p1")
	if err != nil {
		t.Fatalf("read gate: %v", err)
	}
	if gate.Status != domain.GateReserved || gate.ActiveCommandID != plan.Stages[0].CommandID {
		t.Fatalf("gate reservation: status=%s active=%s want command %s",
			gate.Status, gate.ActiveCommandID, plan.Stages[0].CommandID)
	}
}
