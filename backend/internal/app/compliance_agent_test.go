package app

import (
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func complianceToolVisible(req CloudAgentRequest) bool {
	for _, definition := range cloudAgentRegistry().Definitions(cloudAgentToolScope(req, false)) {
		if definition.Name == "compliance_get_check" {
			return true
		}
	}
	return false
}

func complianceAgentRequest(checkID string) CloudAgentRequest {
	req := agentTestRequest()
	req.CanvasID, req.ContextScope = "", nil
	req.ComplianceCheckID = checkID
	return req
}

func seedComplianceCheck(t *testing.T, s *Service, owner, id string) {
	t.Helper()
	now := time.Now().UTC()
	result := `{"overall":"review","faces":[{"index":1,"box":{"x":14,"y":14,"w":30,"h":42},"similarity":78,"bestBaseIndex":0,"verdict":"suspect"}]}`
	check := &model.ComplianceCheck{ID: id, UserID: owner, Kind: compliance.KindFaceCompare, Status: compliance.StatusDone, StepsDone: compliance.StepCount, ResultJSON: result, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateComplianceCheck(check, []model.ComplianceCheckImage{
		{ID: id + "_t", CheckID: id, UserID: owner, Role: model.ComplianceImageTarget, ResourceID: "res_t", Name: "still.png", CreatedAt: now},
		{ID: id + "_b", CheckID: id, UserID: owner, Role: model.ComplianceImageBase, ResourceID: "res_b", Name: "base.png", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudAgentRunBindsOnlyOwnComplianceCheck(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	seedComplianceCheck(t, s, "user", "chk_own")
	seedComplianceCheck(t, s, "someone-else", "chk_foreign")

	req := complianceAgentRequest("chk_own")
	req.IdempotencyKey = "compliance-bound"
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatalf("绑定自己的核验应成功：%v", err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Request.ComplianceLinked || state.Request.ComplianceCheckID != "chk_own" || state.Request.CanvasID != "" {
		t.Fatalf("绑定应被持久化且不绑定画布：%+v", state.Request)
	}
	if !complianceToolVisible(state.Request) {
		t.Fatal("绑定核验后应暴露 compliance_get_check")
	}

	foreign := complianceAgentRequest("chk_foreign")
	foreign.IdempotencyKey = "compliance-foreign"
	var appErr *kernel.AppError
	if _, err := s.CreateCloudAgentRun("user", foreign, ""); !errors.As(err, &appErr) || appErr.Status != 404 {
		t.Fatalf("绑定别人的核验应返回 404，得到 %v", err)
	}
}

func TestComplianceGetCheckToolWaitsForRunningCheck(t *testing.T) {
	s, db, alice, _ := newTestComplianceService(t)
	seedComplianceResource(t, db, alice.ID, "res_target", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_base", "image", model.ResourceStatusReady)
	s.complianceComparer = compliance.MockFaceComparer{StepDelay: 150 * time.Millisecond}
	created, err := s.CreateComplianceCheck(alice, faceCheckRequest("res_target", "res_base"))
	if err != nil || created.Status != compliance.StatusRunning {
		t.Fatalf("刚创建的核验应在运行：%+v (%v)", created, err)
	}
	call := cloudAgentCall{ID: "c1"}
	call.Function.Arguments = `{}`
	state := &cloudAgentRuntime{Request: CloudAgentRequest{ComplianceCheckID: created.ID, ComplianceLinked: true}}
	value, err := cloudAgentHandleComplianceGetCheck(s.repo, alice.ID, state, call, s)
	if err != nil {
		t.Fatal(err)
	}
	if view := value.(map[string]any)["check"].(*ComplianceCheckView); view.Status != compliance.StatusDone || view.Result == nil {
		t.Fatalf("工具应等到核验完成再返回：%+v", view)
	}
}

func TestComplianceGetCheckToolWaitIsBounded(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	now := time.Now().UTC()
	stuck := &model.ComplianceCheck{ID: "chk_stuck", UserID: "user", Kind: compliance.KindFaceCompare, Status: compliance.StatusRunning, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateComplianceCheck(stuck, []model.ComplianceCheckImage{{ID: "chi_1", CheckID: stuck.ID, UserID: "user", Role: model.ComplianceImageTarget, ResourceID: "res_t", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	previous := complianceAgentWait
	complianceAgentWait = 300 * time.Millisecond
	defer func() { complianceAgentWait = previous }()
	call := cloudAgentCall{ID: "c1"}
	call.Function.Arguments = `{}`
	started := time.Now()
	value, err := cloudAgentHandleComplianceGetCheck(s.repo, "user", &cloudAgentRuntime{Request: CloudAgentRequest{ComplianceCheckID: stuck.ID}}, call, s)
	if err != nil || time.Since(started) > 2*time.Second {
		t.Fatalf("等待必须有上限：用时 %v，错误 %v", time.Since(started), err)
	}
	if view := value.(map[string]any)["check"].(*ComplianceCheckView); view.Status != compliance.StatusRunning {
		t.Fatalf("超时后应如实返回仍在运行：%+v", view)
	}
}

func TestCloudAgentRunIgnoresClientComplianceLinkedFlag(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	req := complianceAgentRequest("")
	req.IdempotencyKey = "compliance-forged"
	req.ComplianceLinked = true
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, _ := s.repo.CloudAgent("user", run.ID)
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if state.Request.ComplianceLinked || complianceToolVisible(state.Request) {
		t.Fatalf("客户端传入的 complianceLinked 必须被覆盖：%+v", state.Request)
	}
	if complianceToolVisible(agentTestRequest()) {
		t.Fatal("普通画布对话不应暴露合规工具")
	}
}

func TestComplianceGetCheckToolReadsBoundCheckOnly(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	seedComplianceCheck(t, s, "user", "chk_own")
	call := cloudAgentCall{ID: "c1"}
	call.Function.Name = "compliance_get_check"
	call.Function.Arguments = `{}`

	state := &cloudAgentRuntime{Request: CloudAgentRequest{ComplianceCheckID: "chk_own", ComplianceLinked: true}}
	value, err := cloudAgentHandleComplianceGetCheck(s.repo, "user", state, call, s)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	view, ok := result["check"].(*ComplianceCheckView)
	if !ok || view.ID != "chk_own" || view.Result == nil || view.Result.Overall != compliance.OverallReview || result["source"] != "compliance_db" || result["note"] != complianceAgentNote {
		t.Fatalf("工具应返回绑定核验的结构化结果：%+v", result)
	}
	if view.Thresholds.Match != compliance.MatchThreshold || view.Thresholds.Suspect != compliance.SuspectThreshold {
		t.Fatalf("工具返回的核验应带上判定阈值：%+v", view.Thresholds)
	}

	if _, err := cloudAgentHandleComplianceGetCheck(s.repo, "user", &cloudAgentRuntime{}, call, s); err == nil {
		t.Fatal("没有绑定核验时应报错")
	}
	var appErr *kernel.AppError
	if _, err := cloudAgentHandleComplianceGetCheck(s.repo, "another-user", state, call, s); !errors.As(err, &appErr) || appErr.Status != 404 {
		t.Fatalf("别的用户读取应返回 404，得到 %v", err)
	}
	call.Function.Arguments = `{"checkId":"chk_foreign"}`
	if _, err := cloudAgentHandleComplianceGetCheck(s.repo, "user", state, call, s); err == nil {
		t.Fatal("工具不接受参数，不能用参数改读别的核验")
	}
}
