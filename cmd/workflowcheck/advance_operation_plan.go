package main

import (
	"fmt"
	"net/http"
	"sync"
)

func runAdvanceOperationPlan(client *apiClient) error {
	if _, err := fixtureGate(client, "gate-upstream", "上游泄洪闸", 6, 0.5); err != nil {
		return err
	}
	if _, err := fixtureGate(client, "gate-downstream", "下游泄洪闸", 6, 1.0); err != nil {
		return err
	}

	planA, err := fixturePlan(client, "上下游联动泄洪", "dispatcher-lin", []map[string]any{
		stageSpec("gate-upstream", 2.5, "先抬上游开度"),
		stageSpec("gate-downstream", 3.0, "再放下游流量"),
	})
	if err != nil {
		return err
	}
	planAID, err := stringField(planA, "id")
	if err != nil {
		return err
	}
	if err := expectPlanState(planA, "active", []string{"waiting", "waiting"}); err != nil {
		return err
	}
	if _, exists := planA["stages"].([]any)[0].(map[string]any)["command_id"]; exists {
		return fmt.Errorf("fresh plan must not bind commands to waiting stages")
	}

	commandA1, err := advanceExactlyOnce(client, planAID)
	if err != nil {
		return err
	}
	if err := expectCommandShape(commandA1, "gate-upstream", "pending", 0.5, 2.5); err != nil {
		return err
	}
	commandA1ID, err := stringField(commandA1, "id")
	if err != nil {
		return err
	}

	duplicate, err := client.post("/plans/"+planAID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(duplicate, http.StatusConflict, "plan_stage_in_progress", "reject duplicate advance"); err != nil {
		return err
	}

	downstream, err := readGate(client, "gate-downstream")
	if err != nil {
		return err
	}
	downstreamStatus, err := stringField(downstream, "status")
	if err != nil {
		return err
	}
	if downstreamStatus != "idle" {
		return fmt.Errorf("waiting stage must not occupy its gate, got status %q", downstreamStatus)
	}
	if _, exists := downstream["active_command_id"]; exists {
		return fmt.Errorf("waiting stage must not reserve its gate in advance")
	}

	if err := completeCommand(client, commandA1ID); err != nil {
		return err
	}

	_, commandA2, err := mustAdvance(client, planAID)
	if err != nil {
		return err
	}
	if err := expectCommandShape(commandA2, "gate-downstream", "pending", 1.0, 3.0); err != nil {
		return err
	}
	commandA2ID, err := stringField(commandA2, "id")
	if err != nil {
		return err
	}
	if err := completeCommand(client, commandA2ID); err != nil {
		return err
	}

	planA, err = readPlan(client, planAID)
	if err != nil {
		return err
	}
	if err := expectPlanState(planA, "completed", []string{"completed", "completed"}); err != nil {
		return err
	}

	again, err := client.post("/plans/"+planAID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(again, http.StatusConflict, "plan_already_completed", "reject advance of completed plan"); err != nil {
		return err
	}

	if err := checkBlockedPlanKeepsCompletedStages(client); err != nil {
		return err
	}
	if err := checkConditionChangedPlan(client); err != nil {
		return err
	}
	if err := checkFailedExecutionBlocksPlan(client); err != nil {
		return err
	}
	if err := checkFaultedGateBlocksStageStart(client); err != nil {
		return err
	}
	if err := checkPlanRequestValidation(client); err != nil {
		return err
	}

	fmt.Println("advance-operation-plan: staged plan advanced in order with consistent conflict explanations")
	return nil
}

func checkBlockedPlanKeepsCompletedStages(client *apiClient) error {
	plan, err := fixturePlan(client, "分段推进后段被否决", "dispatcher-lin", []map[string]any{
		stageSpec("gate-upstream", 4.0, "继续抬上游"),
		stageSpec("gate-downstream", 1.5, "收下游开度"),
	})
	if err != nil {
		return err
	}
	planID, err := stringField(plan, "id")
	if err != nil {
		return err
	}

	_, first, err := mustAdvance(client, planID)
	if err != nil {
		return err
	}
	firstID, err := stringField(first, "id")
	if err != nil {
		return err
	}
	if err := completeCommand(client, firstID); err != nil {
		return err
	}

	_, second, err := mustAdvance(client, planID)
	if err != nil {
		return err
	}
	secondID, err := stringField(second, "id")
	if err != nil {
		return err
	}
	blocked, err := client.post("/commands/"+secondID+"/interlock", map[string]any{
		"verdict":  "blocked",
		"verifier": "safety-wu",
		"note":     "下游水位超限",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(blocked, http.StatusOK, "block second stage command"); err != nil {
		return err
	}

	advance, err := client.post("/plans/"+planID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(advance, http.StatusConflict, "plan_blocked", "reject advance after stage failure"); err != nil {
		return err
	}

	plan, err = readPlan(client, planID)
	if err != nil {
		return err
	}
	if err := expectPlanState(plan, "blocked", []string{"completed", "failed"}); err != nil {
		return err
	}

	downstream, err := readGate(client, "gate-downstream")
	if err != nil {
		return err
	}
	downstreamStatus, err := stringField(downstream, "status")
	if err != nil {
		return err
	}
	if downstreamStatus != "idle" {
		return fmt.Errorf("blocked review must not occupy the gate, got status %q", downstreamStatus)
	}
	return nil
}

func checkConditionChangedPlan(client *apiClient) error {
	plan, err := fixturePlan(client, "目标开度已被现场达到", "dispatcher-lin", []map[string]any{
		stageSpec("gate-downstream", 3.0, "下游保持当前开度"),
	})
	if err != nil {
		return err
	}
	planID, err := stringField(plan, "id")
	if err != nil {
		return err
	}

	advance, err := client.post("/plans/"+planID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	return requireErrorCode(advance, http.StatusConflict, "plan_condition_changed", "reject advance when conditions already match")
}

func checkFailedExecutionBlocksPlan(client *apiClient) error {
	if _, err := fixtureGate(client, "gate-aux", "辅助泄洪闸", 3, 0.5); err != nil {
		return err
	}
	plan, err := fixturePlan(client, "执行故障联动", "dispatcher-lin", []map[string]any{
		stageSpec("gate-aux", 2.0, "抬辅助闸"),
	})
	if err != nil {
		return err
	}
	planID, err := stringField(plan, "id")
	if err != nil {
		return err
	}

	_, command, err := mustAdvance(client, planID)
	if err != nil {
		return err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return err
	}
	approved, err := fixtureClearReview(client, commandID, "safety-wu")
	if err != nil {
		return err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return err
	}
	aborted, err := fixtureExecute(client, commandID, token, "operator-chen", true)
	if err != nil {
		return err
	}
	abortedStatus, err := stringField(aborted, "status")
	if err != nil {
		return err
	}
	if abortedStatus != "aborted" {
		return fmt.Errorf("simulated fault status: expected aborted, got %q", abortedStatus)
	}

	advance, err := client.post("/plans/"+planID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(advance, http.StatusConflict, "plan_blocked", "reject advance after aborted stage"); err != nil {
		return err
	}

	plan, err = readPlan(client, planID)
	if err != nil {
		return err
	}
	return expectPlanState(plan, "blocked", []string{"failed"})
}

func checkFaultedGateBlocksStageStart(client *apiClient) error {
	if _, err := fixtureGate(client, "gate-aux2", "备用泄洪闸", 3, 0.5); err != nil {
		return err
	}
	plan, err := fixturePlan(client, "后段闸门故障", "dispatcher-lin", []map[string]any{
		stageSpec("gate-aux2", 1.5, "先动备用闸"),
		stageSpec("gate-aux", 1.0, "再动故障闸"),
	})
	if err != nil {
		return err
	}
	planID, err := stringField(plan, "id")
	if err != nil {
		return err
	}

	_, command, err := mustAdvance(client, planID)
	if err != nil {
		return err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return err
	}
	if err := completeCommand(client, commandID); err != nil {
		return err
	}

	advance, err := client.post("/plans/"+planID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	return requireErrorCode(advance, http.StatusConflict, "gate_faulted", "reject stage start on faulted gate")
}

func checkPlanRequestValidation(client *apiClient) error {
	missing, err := client.get("/plans/plan-missing")
	if err != nil {
		return err
	}
	if err := requireErrorCode(missing, http.StatusNotFound, "plan_not_found", "read unknown plan"); err != nil {
		return err
	}

	emptyStages, err := client.post("/plans", map[string]any{
		"name":         "空计划",
		"requested_by": "dispatcher-lin",
		"stages":       []map[string]any{},
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(emptyStages, http.StatusBadRequest, "invalid_plan_stages", "reject plan without stages"); err != nil {
		return err
	}

	unknownGate, err := client.post("/plans", map[string]any{
		"name":         "未知闸门计划",
		"requested_by": "dispatcher-lin",
		"stages":       []map[string]any{stageSpec("gate-missing", 1.0, "未知闸门")},
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(unknownGate, http.StatusNotFound, "gate_not_found", "reject plan with unknown gate"); err != nil {
		return err
	}
	return nil
}

func stageSpec(gateID string, targetAperture float64, reason string) map[string]any {
	return map[string]any{
		"gate_id":         gateID,
		"target_aperture": targetAperture,
		"reason":          reason,
	}
}

func fixturePlan(
	client *apiClient,
	name string,
	requestedBy string,
	stages []map[string]any,
) (map[string]any, error) {
	result, err := client.post("/plans", map[string]any{
		"name":         name,
		"requested_by": requestedBy,
		"stages":       stages,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture plan"); err != nil {
		return nil, err
	}
	plan, err := objectField(result.Body, "plan")
	if err != nil {
		return nil, fmt.Errorf("create fixture plan response: %w", err)
	}
	return plan, nil
}

func mustAdvance(client *apiClient, planID string) (map[string]any, map[string]any, error) {
	result, err := client.post("/plans/"+planID+"/advance", map[string]any{
		"requested_by": "dispatcher-lin",
	})
	if err != nil {
		return nil, nil, err
	}
	if err := requireStatus(result, http.StatusOK, "advance plan"); err != nil {
		return nil, nil, err
	}
	plan, err := objectField(result.Body, "plan")
	if err != nil {
		return nil, nil, err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return nil, nil, err
	}
	return plan, command, nil
}

func advanceExactlyOnce(client *apiClient, planID string) (map[string]any, error) {
	const attempts = 2
	results := make([]apiResult, attempts)
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = client.post("/plans/"+planID+"/advance", map[string]any{
				"requested_by": "dispatcher-lin",
			})
		}(i)
	}
	wg.Wait()

	var command map[string]any
	winners := 0
	for i := 0; i < attempts; i++ {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if results[i].Status == http.StatusOK {
			winners++
			var err error
			command, err = objectField(results[i].Body, "command")
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := requireErrorCode(
			results[i],
			http.StatusConflict,
			"plan_stage_in_progress",
			"reject concurrent advance",
		); err != nil {
			return nil, err
		}
	}
	if winners != 1 {
		return nil, fmt.Errorf("concurrent advance: expected exactly one winner, got %d", winners)
	}
	return command, nil
}

func completeCommand(client *apiClient, commandID string) error {
	approved, err := fixtureClearReview(client, commandID, "safety-wu")
	if err != nil {
		return err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return err
	}
	completed, err := fixtureExecute(client, commandID, token, "operator-chen", false)
	if err != nil {
		return err
	}
	status, err := stringField(completed, "status")
	if err != nil {
		return err
	}
	if status != "completed" {
		return fmt.Errorf("command completion: expected completed, got %q", status)
	}
	return nil
}

func readPlan(client *apiClient, planID string) (map[string]any, error) {
	result, err := client.get("/plans/" + planID)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read plan"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "plan")
}

func readGate(client *apiClient, gateID string) (map[string]any, error) {
	result, err := client.get("/gates/" + gateID)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read gate"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "gate")
}

func expectPlanState(plan map[string]any, status string, stageStatuses []string) error {
	actual, err := stringField(plan, "status")
	if err != nil {
		return err
	}
	if actual != status {
		return fmt.Errorf("plan status: expected %q, got %q", status, actual)
	}
	stages, err := arrayField(plan, "stages")
	if err != nil {
		return err
	}
	if len(stages) != len(stageStatuses) {
		return fmt.Errorf("plan stages: expected %d, got %d", len(stageStatuses), len(stages))
	}
	for i, expected := range stageStatuses {
		stage, ok := stages[i].(map[string]any)
		if !ok {
			return fmt.Errorf("plan stage %d is not an object", i+1)
		}
		stageStatus, err := stringField(stage, "status")
		if err != nil {
			return err
		}
		if stageStatus != expected {
			return fmt.Errorf("plan stage %d status: expected %q, got %q", i+1, expected, stageStatus)
		}
	}
	return nil
}

func expectCommandShape(
	command map[string]any,
	gateID string,
	status string,
	startingAperture float64,
	targetAperture float64,
) error {
	actualGate, err := stringField(command, "gate_id")
	if err != nil {
		return err
	}
	if actualGate != gateID {
		return fmt.Errorf("stage command gate: expected %q, got %q", gateID, actualGate)
	}
	actualStatus, err := stringField(command, "status")
	if err != nil {
		return err
	}
	if actualStatus != status {
		return fmt.Errorf("stage command status: expected %q, got %q", status, actualStatus)
	}
	actualStarting, err := floatField(command, "starting_aperture")
	if err != nil {
		return err
	}
	if actualStarting != startingAperture {
		return fmt.Errorf("stage command starting aperture: expected %v, got %v", startingAperture, actualStarting)
	}
	actualTarget, err := floatField(command, "target_aperture")
	if err != nil {
		return err
	}
	if actualTarget != targetAperture {
		return fmt.Errorf("stage command target aperture: expected %v, got %v", targetAperture, actualTarget)
	}
	return nil
}
