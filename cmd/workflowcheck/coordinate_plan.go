package main

import (
	"fmt"
	"net/http"
)

// runCoordinateOperationPlan verifies the staged coordination workflow:
// stages only start in business order, each start re-confirms the real
// completion of the previous stage command and the current gate conditions,
// failures halt the plan without rewriting completed history, and later
// stages never reserve their gates early.
func runCoordinateOperationPlan(client *apiClient) error {
	gates := []struct {
		id      string
		name    string
		max     float64
		initial float64
	}{
		{"gate-a", "一号闸门", 5, 0},
		{"gate-b", "二号闸门", 5, 0},
		{"gate-c", "三号闸门", 5, 0},
		{"gate-d", "四号闸门", 5, 0},
	}
	for _, fixture := range gates {
		if _, err := fixtureGate(client, fixture.id, fixture.name, fixture.max, fixture.initial); err != nil {
			return err
		}
	}

	created, err := client.post("/plans", map[string]any{
		"id":           "plan-chain-1",
		"name":         "上下游联动调整",
		"requested_by": "dispatcher-lin",
		"stages": []map[string]any{
			{"gate_id": "gate-a", "target_aperture": 2.0, "reason": "第一阶段调整"},
			{"gate_id": "gate-b", "target_aperture": 1.5, "reason": "第二阶段调整"},
			{"gate_id": "gate-c", "target_aperture": 3.0, "reason": "第三阶段调整"},
			{"gate_id": "gate-d", "target_aperture": 1.0, "reason": "第四阶段调整"},
		},
	})
	if err != nil {
		return err
	}
	if err := requireStatus(created, http.StatusCreated, "create coordinated plan"); err != nil {
		return err
	}
	plan, err := objectField(created.Body, "plan")
	if err != nil {
		return fmt.Errorf("create plan response: %w", err)
	}
	if status, _ := stringField(plan, "status"); status != "active" {
		return fmt.Errorf("new plan status: expected active, got %q", status)
	}
	stages, err := sliceField(plan, "stages")
	if err != nil {
		return err
	}
	if len(stages) != 4 {
		return fmt.Errorf("new plan stages: expected 4, got %d", len(stages))
	}
	for index, stage := range stages {
		status, _ := stringField(stage, "status")
		if status != "waiting" {
			return fmt.Errorf("stage %d status before any advance: expected waiting, got %q", index+1, status)
		}
	}

	// Registration only captures intent: no later gate may be reserved early.
	for _, fixture := range gates {
		if err := expectGateIdle(client, fixture.id); err != nil {
			return err
		}
	}

	// A later stage cannot start before its predecessor really completes.
	if err := expectAdvanceConflict(client, "plan-chain-1", 2, "safety-wu",
		"advance stage 2 before stage 1", "predecessor_not_completed"); err != nil {
		return err
	}

	// Condition change: an unrelated command moves gate-a before stage 1 starts.
	external, err := fixtureCommand(client, "gate-a", 1.0, "外部临时调整", "dispatcher-qin")
	if err != nil {
		return err
	}
	externalID, err := stringField(external, "id")
	if err != nil {
		return err
	}
	externalApproved, err := fixtureClearReview(client, externalID, "safety-wu")
	if err != nil {
		return err
	}
	externalToken, err := stringField(externalApproved, "execution_token")
	if err != nil {
		return err
	}
	executed, err := client.post("/commands/"+externalID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          externalToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}

	// While gate-a is reserved by that command, stage 1 cannot reserve it.
	// The external command settles synchronously, so keep a second long-lived
	// external command on gate-a to prove the advance is blocked and writes
	// nothing.
	if err := requireStatus(executed, http.StatusOK, "execute external gate-a command"); err != nil {
		return err
	}
	blocking, err := fixtureCommand(client, "gate-a", 0.5, "占位的冲突命令", "dispatcher-qin")
	if err != nil {
		return err
	}
	blockingID, err := stringField(blocking, "id")
	if err != nil {
		return err
	}
	blockingApproved, err := fixtureClearReview(client, blockingID, "safety-wu")
	if err != nil {
		return err
	}
	blockingToken, err := stringField(blockingApproved, "execution_token")
	if err != nil {
		return err
	}
	if err := expectAdvanceConflict(client, "plan-chain-1", 1, "safety-wu",
		"advance stage 1 while gate is reserved", "gate_not_idle"); err != nil {
		return err
	}
	if err := expectStageStatus(client, "plan-chain-1", 1, "waiting"); err != nil {
		return fmt.Errorf("blocked advance must not start the stage: %w", err)
	}

	// Release the external occupant; stage 1 now starts and freezes the
	// aperture observed at that moment (1.0), not the registration-time value.
	release, err := client.post("/commands/"+blockingID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          blockingToken,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(release, http.StatusOK, "release blocking command"); err != nil {
		return err
	}
	if releaseErr := expectGateAperture(client, "gate-a", "idle", 0.5); releaseErr != nil {
		return releaseErr
	}
	armed1, err := client.advance("plan-chain-1", 1, "safety-wu")
	if err != nil {
		return err
	}
	if err := requireStatus(armed1, http.StatusOK, "advance stage 1 after gate frees"); err != nil {
		return err
	}
	command1, err := objectField(armed1.Body, "command")
	if err != nil {
		return err
	}
	if starting, _ := floatField(command1, "starting_aperture"); starting != 0.5 {
		return fmt.Errorf("stage 1 starting aperture: expected current 0.5, got %v", starting)
	}
	if target, _ := floatField(command1, "target_aperture"); target != 2.0 {
		return fmt.Errorf("stage 1 target aperture: expected 2.0, got %v", target)
	}
	if planID, _ := stringField(command1, "plan_id"); planID != "plan-chain-1" {
		return fmt.Errorf("stage command plan link: expected plan-chain-1, got %q", planID)
	}
	token1, err := stringField(command1, "execution_token")
	if err != nil {
		return err
	}
	if token1 == "" {
		return fmt.Errorf("stage 1 advance did not issue an execution token")
	}
	command1ID, err := stringField(command1, "id")
	if err != nil {
		return err
	}
	if err := expectStageStatus(client, "plan-chain-1", 1, "armed"); err != nil {
		return err
	}
	if err := expectGateReserved(client, "gate-a", command1ID); err != nil {
		return err
	}
	// Gates of later stages stay untouched while stage 1 is only armed.
	if err := expectGateIdle(client, "gate-b"); err != nil {
		return err
	}
	if err := expectGateIdle(client, "gate-c"); err != nil {
		return err
	}

	// Repeated advancement is a stable conflict, not a second reservation.
	if err := expectAdvanceConflict(client, "plan-chain-1", 1, "safety-wu",
		"re-advance armed stage 1", "stage_already_armed"); err != nil {
		return err
	}

	// Plan-bound commands cannot bypass the plan stage endpoint.
	bypass, err := client.post("/commands/"+command1ID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token1,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := expectConflict(bypass, "execute plan command via standalone endpoint", "plan_bound_command"); err != nil {
		return err
	}

	// Wrong token must not move anything.
	if err := expectExecuteConflict(client, "plan-chain-1", 1, "operator-chen", "not-the-token", false,
		"execute stage 1 with wrong token", "invalid_execution_token"); err != nil {
		return err
	}
	if err := expectStageStatus(client, "plan-chain-1", 1, "armed"); err != nil {
		return fmt.Errorf("failed execution must leave stage armed: %w", err)
	}
	if err := expectGateReserved(client, "gate-a", command1ID); err != nil {
		return err
	}

	// Real execution completes stage 1.
	done1, err := client.executeStage("plan-chain-1", 1, "operator-chen", token1, false)
	if err != nil {
		return err
	}
	if err := requireStatus(done1, http.StatusOK, "execute stage 1"); err != nil {
		return err
	}
	if err := expectStageStatus(client, "plan-chain-1", 1, "completed"); err != nil {
		return err
	}
	if err := expectGateAperture(client, "gate-a", "idle", 2.0); err != nil {
		return err
	}

	// Token reuse and repeated advancement after completion.
	if err := expectExecuteConflict(client, "plan-chain-1", 1, "operator-chen", token1, false,
		"reject reused stage token", "stage_not_armed"); err != nil {
		return err
	}
	if err := expectAdvanceConflict(client, "plan-chain-1", 1, "safety-wu",
		"re-advance completed stage 1", "stage_already_completed"); err != nil {
		return err
	}
	// Stage 3 still cannot skip stage 2.
	if err := expectAdvanceConflict(client, "plan-chain-1", 3, "safety-wu",
		"advance stage 3 before stage 2", "predecessor_not_completed"); err != nil {
		return err
	}

	// Stage 2 starts only after the real completion of stage 1, and reserves
	// exactly one gate; stage 3 and 4 gates remain idle.
	armed2, err := client.advance("plan-chain-1", 2, "safety-he")
	if err != nil {
		return err
	}
	if err := requireStatus(armed2, http.StatusOK, "advance stage 2"); err != nil {
		return err
	}
	command2, err := objectField(armed2.Body, "command")
	if err != nil {
		return err
	}
	token2, _ := stringField(command2, "execution_token")
	command2ID, _ := stringField(command2, "id")
	if starting, _ := floatField(command2, "starting_aperture"); starting != 0 {
		return fmt.Errorf("stage 2 starting aperture: expected 0, got %v", starting)
	}
	if err := expectGateReserved(client, "gate-b", command2ID); err != nil {
		return err
	}
	if err := expectGateIdle(client, "gate-c"); err != nil {
		return err
	}
	if err := expectGateIdle(client, "gate-d"); err != nil {
		return err
	}
	// An unrelated clear review cannot steal a gate owned by a plan stage.
	rival, err := fixtureCommand(client, "gate-b", 2.5, "抢占已占用闸门", "dispatcher-qin")
	if err != nil {
		return err
	}
	rivalID, _ := stringField(rival, "id")
	rivalReview, err := client.post("/commands/"+rivalID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": "safety-he",
		"note":     "并发抢占",
	})
	if err != nil {
		return err
	}
	if err := expectConflict(rivalReview, "rival review on plan-reserved gate", "gate_not_idle"); err != nil {
		return err
	}

	done2, err := client.executeStage("plan-chain-1", 2, "operator-chen", token2, false)
	if err != nil {
		return err
	}
	if err := requireStatus(done2, http.StatusOK, "execute stage 2"); err != nil {
		return err
	}
	if err := expectStageStatus(client, "plan-chain-1", 2, "completed"); err != nil {
		return err
	}
	if err := expectGateAperture(client, "gate-b", "idle", 1.5); err != nil {
		return err
	}

	// Stage 3 fails: command aborts, gate latches faulted, plan halts, and the
	// already completed stages must not be rewritten.
	armed3, err := client.advance("plan-chain-1", 3, "safety-he")
	if err != nil {
		return err
	}
	if err := requireStatus(armed3, http.StatusOK, "advance stage 3"); err != nil {
		return err
	}
	command3, err := objectField(armed3.Body, "command")
	if err != nil {
		return err
	}
	token3, _ := stringField(command3, "execution_token")
	failed3, err := client.executeStage("plan-chain-1", 3, "operator-chen", token3, true)
	if err != nil {
		return err
	}
	if err := requireStatus(failed3, http.StatusOK, "execute stage 3 with simulated fault"); err != nil {
		return err
	}
	failedCommand, err := objectField(failed3.Body, "command")
	if err != nil {
		return err
	}
	if status, _ := stringField(failedCommand, "status"); status != "aborted" {
		return fmt.Errorf("faulted stage command: expected aborted, got %q", status)
	}
	if err := expectGateAperture(client, "gate-c", "faulted", 3.0); err != nil {
		return err
	}

	finalPlan, err := getPlan(client, "plan-chain-1")
	if err != nil {
		return err
	}
	if status, _ := stringField(finalPlan, "status"); status != "halted" {
		return fmt.Errorf("plan after stage failure: expected halted, got %q", status)
	}
	if reason, _ := stringField(finalPlan, "halt_reason"); reason == "" {
		return fmt.Errorf("halted plan must record a halt reason")
	}
	finalStages, err := sliceField(finalPlan, "stages")
	if err != nil {
		return err
	}
	expected := []string{"completed", "completed", "failed", "waiting"}
	for index, want := range expected {
		got, _ := stringField(finalStages[index], "status")
		if got != want {
			return fmt.Errorf("final stage %d status: expected %s, got %q", index+1, want, got)
		}
	}
	// Completed history keeps its real results despite the later blockage.
	if err := expectGateAperture(client, "gate-a", "idle", 2.0); err != nil {
		return err
	}
	if err := expectGateAperture(client, "gate-b", "idle", 1.5); err != nil {
		return err
	}
	// The un-started stage 4 never occupied its gate.
	if err := expectGateIdle(client, "gate-d"); err != nil {
		return err
	}

	// After halt nothing can advance or execute further, even with a token.
	if err := expectAdvanceConflict(client, "plan-chain-1", 4, "safety-he",
		"advance stage 4 after halt", "plan_halted"); err != nil {
		return err
	}
	if err := expectAdvanceConflict(client, "plan-chain-1", 3, "safety-he",
		"re-advance failed stage 3", "plan_halted"); err != nil {
		return err
	}
	if err := expectGateIdle(client, "gate-d"); err != nil {
		return err
	}

	fmt.Println("coordinate-operation-plan: stages advanced in order, failures halted the plan without rewriting history")
	return nil
}

func (c *apiClient) advance(planID string, sequence int, confirmedBy string) (apiResult, error) {
	return c.post(fmt.Sprintf("/plans/%s/stages/%d/advance", planID, sequence), map[string]any{
		"confirmed_by": confirmedBy,
		"note":         "按阶段条件重新确认",
	})
}

func (c *apiClient) executeStage(planID string, sequence int, operator, token string, simulateFault bool) (apiResult, error) {
	return c.post(fmt.Sprintf("/plans/%s/stages/%d/execute", planID, sequence), map[string]any{
		"operator":       operator,
		"token":          token,
		"simulate_fault": simulateFault,
	})
}

func getPlan(client *apiClient, id string) (map[string]any, error) {
	result, err := client.get("/plans/" + id)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read plan"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "plan")
}

func planStage(client *apiClient, planID string, sequence int) (map[string]any, error) {
	plan, err := getPlan(client, planID)
	if err != nil {
		return nil, err
	}
	stages, err := sliceField(plan, "stages")
	if err != nil {
		return nil, err
	}
	if sequence < 1 || sequence > len(stages) {
		return nil, fmt.Errorf("stage %d is outside the plan", sequence)
	}
	return stages[sequence-1], nil
}

func expectStageStatus(client *apiClient, planID string, sequence int, expected string) error {
	stage, err := planStage(client, planID, sequence)
	if err != nil {
		return err
	}
	status, err := stringField(stage, "status")
	if err != nil {
		return err
	}
	if status != expected {
		return fmt.Errorf("stage %d status: expected %s, got %q", sequence, expected, status)
	}
	return nil
}

func expectGateIdle(client *apiClient, gateID string) error {
	return expectGateAperture(client, gateID, "idle", -1)
}

func expectGateReserved(client *apiClient, gateID, commandID string) error {
	result, err := client.get("/gates/" + gateID)
	if err != nil {
		return err
	}
	if err := requireStatus(result, http.StatusOK, "read gate"); err != nil {
		return err
	}
	gate, err := objectField(result.Body, "gate")
	if err != nil {
		return err
	}
	status, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	active, err := stringField(gate, "active_command_id")
	if err != nil {
		return err
	}
	if status != "reserved" || active != commandID {
		return fmt.Errorf("gate %s: expected reserved by %s, got status=%q active=%q", gateID, commandID, status, active)
	}
	return nil
}

func expectGateAperture(client *apiClient, gateID, status string, aperture float64) error {
	result, err := client.get("/gates/" + gateID)
	if err != nil {
		return err
	}
	if err := requireStatus(result, http.StatusOK, "read gate"); err != nil {
		return err
	}
	gate, err := objectField(result.Body, "gate")
	if err != nil {
		return err
	}
	gotStatus, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	if gotStatus != status {
		return fmt.Errorf("gate %s status: expected %s, got %q", gateID, status, gotStatus)
	}
	if aperture >= 0 {
		gotAperture, err := floatField(gate, "aperture")
		if err != nil {
			return err
		}
		if gotAperture != aperture {
			return fmt.Errorf("gate %s aperture: expected %v, got %v", gateID, aperture, gotAperture)
		}
	}
	return nil
}

func expectConflict(result apiResult, operation, code string) error {
	if result.Status != http.StatusConflict {
		return fmt.Errorf("%s: expected HTTP 409, got %d with body %s", operation, result.Status, prettyBody(result.Body))
	}
	gotCode, _ := stringField(result.Body, "code")
	if gotCode != code {
		return fmt.Errorf("%s: expected error code %q, got %q", operation, code, gotCode)
	}
	return nil
}

func expectAdvanceConflict(client *apiClient, planID string, sequence int, confirmedBy, operation, code string) error {
	result, err := client.advance(planID, sequence, confirmedBy)
	if err != nil {
		return err
	}
	return expectConflict(result, operation, code)
}

func expectExecuteConflict(client *apiClient, planID string, sequence int, operator, token string, simulateFault bool, operation, code string) error {
	result, err := client.executeStage(planID, sequence, operator, token, simulateFault)
	if err != nil {
		return err
	}
	return expectConflict(result, operation, code)
}

func sliceField(object map[string]any, name string) ([]map[string]any, error) {
	value, exists := object[name]
	if !exists {
		return nil, fmt.Errorf("object is missing array field %q", name)
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("object field %q is not an array", name)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("array field %q contains a non-object entry", name)
		}
		items = append(items, entry)
	}
	return items, nil
}
