package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agenttool "yingce/backend/internal/agent/tool"
	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

var complianceToolNames = []string{"compliance_get_check", "compliance_inspect_images", "compliance_detect_faces", "compliance_compare_faces", "compliance_submit_report"}

func visibleToolNames(req CloudAgentRequest) map[string]bool {
	names := map[string]bool{}
	for _, definition := range cloudAgentRegistry().Definitions(cloudAgentToolScope(req, false)) {
		names[definition.Name] = true
	}
	return names
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
	report := `{"overall":"review","style":"realistic","summary":"s","faces":[{"index":1,"box":{"x":14,"y":14,"w":30,"h":42},"similarity":78,"bestBaseIndex":0,"verdict":"suspect","basis":["b"],"limitations":[]}],"suggestions":[],"humanReview":{"required":true},"disclaimer":"d"}`
	check := &model.ComplianceCheck{ID: id, UserID: owner, Kind: compliance.KindFaceCompare, Status: compliance.StatusDone, StepsDone: compliance.StepCount, ResultJSON: report, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateComplianceCheck(check, []model.ComplianceCheckImage{
		{ID: id + "_t", CheckID: id, UserID: owner, Role: model.ComplianceImageTarget, ResourceID: "res_t", Name: "still.png", CreatedAt: now},
		{ID: id + "_b", CheckID: id, UserID: owner, Role: model.ComplianceImageBase, ResourceID: "res_b", Name: "base.png", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudAgentRunBindsOnlyOwnComplianceCheck(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	if err := s.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
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
	if state.SkillVersions[complianceSkillID] == "" {
		t.Fatalf("绑定核验的运行应冻结合规技能：%v", state.SkillVersions)
	}

	foreign := complianceAgentRequest("chk_foreign")
	foreign.IdempotencyKey = "compliance-foreign"
	var appErr *kernel.AppError
	if _, err := s.CreateCloudAgentRun("user", foreign, ""); !errors.As(err, &appErr) || appErr.Status != 404 {
		t.Fatalf("绑定别人的核验应返回 404，得到 %v", err)
	}
}

func TestComplianceToolsAreOnlyVisibleToBoundRuns(t *testing.T) {
	bound := complianceAgentRequest("chk")
	bound.ComplianceLinked, bound.VisionEnabled = true, true
	names := visibleToolNames(bound)
	for _, name := range complianceToolNames {
		if !names[name] {
			t.Errorf("绑定核验且开放视觉的运行应暴露 %s", name)
		}
	}
	for name := range names {
		if strings.HasPrefix(name, "canvas_") || name == "generate_media" || name == "memory_update" {
			t.Errorf("合规运行不应暴露画布或写入工具 %s", name)
		}
	}

	bound.VisionEnabled = false
	if visibleToolNames(bound)["compliance_inspect_images"] {
		t.Error("模型不支持图片输入时不应暴露看图工具")
	}
	canvas := agentTestRequest()
	canvas.VisionEnabled = true
	for name := range visibleToolNames(canvas) {
		if strings.HasPrefix(name, "compliance_") {
			t.Errorf("普通画布对话不应暴露 %s", name)
		}
	}
}

func TestComplianceToolsNeverNeedApprovalAndStayOutOfReadBudget(t *testing.T) {
	for _, name := range complianceToolNames[1:] {
		policy := agenttool.Of(name)
		if policy.ApprovalRequired("request_approval") || policy.Write() || policy.ReadOnly() {
			t.Errorf("%s 记录证据但不是写入也不是只读：%+v", name, policy)
		}
	}
	if !agenttool.Of("compliance_get_check").ReadOnly() {
		t.Error("compliance_get_check 是只读工具")
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
	if state.Request.ComplianceLinked || state.SkillVersions[complianceSkillID] != "" {
		t.Fatalf("客户端传入的 complianceLinked 必须被覆盖，也不会注入合规技能：%+v", state.Request)
	}
}

func TestComplianceSkillIsReadableWithoutInstallingOnlyInBoundRuns(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	execution, err := f.s.repo.CloudAgent(complianceTestUser, created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &cloudAgentRuntime{Request: state.Request, SkillVersions: state.SkillVersions}
	page, err := f.s.cloudAgentReadSkillFile(complianceTestUser, runtime, agenttool.SkillReadArgs{SkillID: complianceSkillID})
	if err != nil {
		t.Fatalf("绑定核验的运行应能读到没安装的合规技能：%v", err)
	}
	if _, ok := page.(map[string]any); !ok {
		t.Fatalf("应返回技能目录：%+v", page)
	}
	entry, err := f.s.cloudAgentReadSkillFile(complianceTestUser, runtime, agenttool.SkillReadArgs{SkillID: complianceSkillID, Path: "SKILL.md"})
	if err != nil || !strings.Contains(complianceJSON(t, entry), "compliance_submit_report") {
		t.Fatalf("SKILL.md 应描述提交流程：%v", err)
	}

	unbound := &cloudAgentRuntime{Request: CloudAgentRequest{ComplianceLinked: false}, SkillVersions: state.SkillVersions}
	if _, err := f.s.cloudAgentReadSkillFile(complianceTestUser, unbound, agenttool.SkillReadArgs{SkillID: complianceSkillID, Path: "SKILL.md"}); err == nil {
		t.Fatal("没绑定核验的运行不能借此读取未安装的技能")
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

func TestComplianceGetCheckDoesNotBlockWhileRunning(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	started := time.Now()
	value, err := cloudAgentHandleComplianceGetCheck(f.s.repo, complianceTestUser, complianceState(created.ID), complianceCall("compliance_get_check", map[string]any{}), f.s)
	if err != nil || time.Since(started) > time.Second {
		t.Fatalf("运行中的核验就是 Agent 自己在做的事，读取不能等待：%v / %v", err, time.Since(started))
	}
	if view := value.(map[string]any)["check"].(*ComplianceCheckView); view.Status != compliance.StatusRunning || len(view.Steps) != compliance.StepCount {
		t.Fatalf("应返回当前进度：%+v", view)
	}
}

func complianceJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
