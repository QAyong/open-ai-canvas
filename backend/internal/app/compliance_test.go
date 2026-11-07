package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// scriptedEngine 让测试控制引擎结果：每张图一张脸，相似度固定；同时统计调用次数。
type scriptedEngine struct {
	similarity int
	faces      int
	detects    atomic.Int32
	compares   atomic.Int32
	failNext   atomic.Bool
}

func (e *scriptedEngine) Detect(_ context.Context, _ compliance.ImageRef) ([]compliance.DetectedFace, error) {
	e.detects.Add(1)
	if e.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("引擎故障")
	}
	faces := make([]compliance.DetectedFace, 0, e.faces)
	for i := 0; i < e.faces; i++ {
		faces = append(faces, compliance.DetectedFace{Index: i + 1, Box: compliance.FaceBox{X: float64(10 + i*40), Y: 10, W: 30, H: 40}, Quality: 90})
	}
	return faces, nil
}

func (e *scriptedEngine) Compare(_ context.Context, _, _ compliance.FaceRef) (int, error) {
	e.compares.Add(1)
	return e.similarity, nil
}

type complianceFixture struct {
	t      *testing.T
	s      *Service
	db     *gorm.DB
	user   *model.User
	engine *scriptedEngine
}

const complianceTestUser = "user"

// newComplianceFixture 搭一个能真正创建 Agent 运行的服务：带支持图片输入的渠道模型、内置技能和三张已上传的图片。
func newComplianceFixture(t *testing.T) *complianceFixture {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	capability.Text.References.MaxImages = 4
	capability.Text.References.MaxImageBytes = 1024 * 1024
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	engine := &scriptedEngine{similarity: 91, faces: 1}
	s.complianceEngine = engine
	fixture := &complianceFixture{t: t, s: s, db: db, user: &model.User{ID: complianceTestUser}, engine: engine}
	for _, id := range []string{"res_target", "res_base1", "res_base2"} {
		fixture.resource(complianceTestUser, id, "image", model.ResourceStatusReady)
	}
	return fixture
}

func (f *complianceFixture) resource(owner, id, kind string, status model.ResourceStatus) {
	f.t.Helper()
	if err := f.db.Create(&model.Resource{ID: id, UserID: owner, Kind: kind, Status: status, MimeType: "image/png", Size: 100, Width: 64, Height: 64}).Error; err != nil {
		f.t.Fatal(err)
	}
}

func faceCheckRequest(target string, bases ...string) CreateComplianceCheckRequest {
	req := CreateComplianceCheckRequest{Kind: compliance.KindFaceCompare, SubjectName: "张三", Target: ComplianceImageInput{ResourceID: target, Name: "still.png"}, AgentModel: ComplianceAgentModel{ChannelID: "channel", ChannelModelKey: "text-test"}}
	for _, base := range bases {
		req.Bases = append(req.Bases, ComplianceImageInput{ResourceID: base, Name: base + ".png"})
	}
	return req
}

func (f *complianceFixture) create(t *testing.T, bases ...string) *ComplianceCheckView {
	t.Helper()
	if len(bases) == 0 {
		bases = []string{"res_base1"}
	}
	view, err := f.s.CreateComplianceCheck(f.user, faceCheckRequest("res_target", bases...))
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func complianceState(checkID string) *cloudAgentRuntime {
	return &cloudAgentRuntime{Request: CloudAgentRequest{ComplianceCheckID: checkID, ComplianceLinked: true, ChannelID: "channel", ChannelModelKey: "text-test", VisionEnabled: true}}
}

func complianceCall(name string, args any) cloudAgentCall {
	raw, _ := json.Marshal(args)
	call := cloudAgentCall{ID: "call-" + name}
	call.Function.Name = name
	call.Function.Arguments = string(raw)
	return call
}

func (f *complianceFixture) detect(t *testing.T, userID, checkID, image string) (any, error) {
	t.Helper()
	return cloudAgentHandleComplianceDetectFaces(f.s.repo, userID, complianceState(checkID), complianceCall("compliance_detect_faces", map[string]any{"image": image}), f.s)
}

func (f *complianceFixture) compare(t *testing.T, userID, checkID string, targetFace int, base string, baseFace int) (any, error) {
	t.Helper()
	return cloudAgentHandleComplianceCompareFaces(f.s.repo, userID, complianceState(checkID), complianceCall("compliance_compare_faces", map[string]any{"targetFace": targetFace, "base": base, "baseFace": baseFace}), f.s)
}

func (f *complianceFixture) submit(t *testing.T, userID, checkID string, input map[string]any) (any, error) {
	t.Helper()
	return cloudAgentHandleComplianceSubmitReport(f.s.repo, userID, complianceState(checkID), complianceCall("compliance_submit_report", input), f.s)
}

func validReport(faces ...int) map[string]any {
	notes := make([]map[string]any, 0, len(faces))
	for _, index := range faces {
		notes = append(notes, map[string]any{"index": index, "basis": []string{"脸型与五官布局相近"}})
	}
	return map[string]any{"style": "realistic", "summary": "已完成比对。", "faces": notes}
}

// runFullFlow 走完检测、比对、提交，返回完成后的核验。
func (f *complianceFixture) runFullFlow(t *testing.T, checkID string) *ComplianceCheckView {
	t.Helper()
	for _, image := range []string{"target", "base-1"} {
		if _, err := f.detect(t, complianceTestUser, checkID, image); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.compare(t, complianceTestUser, checkID, 1, "base-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.submit(t, complianceTestUser, checkID, validReport(1)); err != nil {
		t.Fatal(err)
	}
	view, err := f.s.ComplianceCheck(complianceTestUser, checkID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func appErrorStatus(err error) int {
	var appErr *kernel.AppError
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 0
}

func TestCreateComplianceCheckStartsAgentRunWithSystemSkill(t *testing.T) {
	f := newComplianceFixture(t)
	view := f.create(t, "res_base1", "res_base2")
	if view.Status != compliance.StatusRunning || view.Target.ResourceID != "res_target" || len(view.Bases) != 2 || len(view.Steps) != compliance.StepCount || view.Steps[0].State != "active" || view.Result != nil {
		t.Fatalf("新建的核验应在运行且第一步进行中：%+v", view)
	}
	if view.RunID != cloudAgentID(complianceTestUser, complianceRunKey(view.ID)) {
		t.Fatalf("核验应记录承载它的 Agent 运行：%s", view.RunID)
	}
	execution, err := f.s.repo.CloudAgent(complianceTestUser, view.RunID)
	if err != nil {
		t.Fatalf("应已创建 Agent 运行：%v", err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	req := state.Request
	if !req.ComplianceLinked || req.ComplianceCheckID != view.ID || req.CanvasID != "" || req.PermissionMode != "read_only" || !req.VisionEnabled || req.Budget.MaxSteps != complianceRunMaxSteps {
		t.Fatalf("运行应绑定核验、只读、不绑画布、开放视觉：%+v", req)
	}
	if state.SkillVersions[complianceSkillID] == "" || view.SkillVersion != state.SkillVersions[complianceSkillID] {
		t.Fatalf("运行应冻结合规技能版本并记到核验上：%v / %q", state.SkillVersions, view.SkillVersion)
	}
	if !strings.Contains(req.Prompt, "compliance_submit_report") || !strings.Contains(req.Prompt, "张三") {
		t.Fatalf("任务描述应带上授权人并指向提交工具：%s", req.Prompt)
	}
}

func TestCreateComplianceCheckRejectsModelWithoutImageInput(t *testing.T) {
	f := newComplianceFixture(t)
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test"))).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateComplianceCheck(f.user, faceCheckRequest("res_target", "res_base1")); appErrorStatus(err) != 400 {
		t.Fatalf("不支持图片输入的模型应返回 400，得到 %v", err)
	}
	req := faceCheckRequest("res_target", "res_base1")
	req.AgentModel = ComplianceAgentModel{}
	if _, err := f.s.CreateComplianceCheck(f.user, req); appErrorStatus(err) != 400 {
		t.Fatalf("没有选模型应返回 400，得到 %v", err)
	}
	var count int64
	f.db.Model(&model.ComplianceCheck{}).Count(&count)
	if count != 0 {
		t.Fatalf("被拒绝的请求不应留下核验记录，现有 %d 条", count)
	}
}

func TestCreateComplianceCheckRejectsForeignAndUnreadyResources(t *testing.T) {
	f := newComplianceFixture(t)
	f.resource("someone-else", "res_bob", "image", model.ResourceStatusReady)
	f.resource(complianceTestUser, "res_video", "video", model.ResourceStatusReady)
	f.resource(complianceTestUser, "res_pending", "image", model.ResourceStatusPending)

	for name, req := range map[string]CreateComplianceCheckRequest{
		"别人的资源":  faceCheckRequest("res_target", "res_bob"),
		"不是图片":   faceCheckRequest("res_target", "res_video"),
		"还没上传完成": faceCheckRequest("res_target", "res_pending"),
		"资源不存在":  faceCheckRequest("res_target", "res_missing"),
		"没有底图":   faceCheckRequest("res_target"),
		"底图超过上限": faceCheckRequest("res_target", "a", "b", "c", "d"),
		"目标缺少资源": faceCheckRequest("", "res_target"),
		"底图重复":   faceCheckRequest("res_target", "res_target", "res_target"),
	} {
		if _, err := f.s.CreateComplianceCheck(f.user, req); appErrorStatus(err) != 400 {
			t.Errorf("%s：应返回 400，得到 %v", name, err)
		}
	}
	var count int64
	f.db.Model(&model.ComplianceCheck{}).Count(&count)
	if count != 0 {
		t.Fatalf("被拒绝的请求不应留下核验记录，现有 %d 条", count)
	}
}

func TestComplianceCheckIsOnlyVisibleToOwner(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	if _, err := f.s.ComplianceCheck("someone-else", created.ID); appErrorStatus(err) != 404 {
		t.Fatalf("别人读取应返回 404，得到 %v", err)
	}
	if _, err := f.s.CreateComplianceCheck(nil, faceCheckRequest("res_target", "res_base1")); appErrorStatus(err) != 401 {
		t.Fatalf("未登录应返回 401，得到 %v", err)
	}
}

func TestComplianceFlowProducesReportFromEngineEvidence(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	done := f.runFullFlow(t, created.ID)
	if done.Status != compliance.StatusDone || done.Result == nil {
		t.Fatalf("提交后核验应完成：%+v", done)
	}
	face := done.Result.Faces[0]
	if done.Result.Overall != compliance.OverallPass || face.Similarity != 91 || face.Verdict != compliance.FaceMatch || face.Box.W != 30 || face.BestBaseIndex != 0 || done.Result.Disclaimer == "" {
		t.Fatalf("数字、人脸框和结论应来自引擎证据与阈值：%+v", done.Result)
	}
	for _, step := range done.Steps {
		if step.State != "done" {
			t.Fatalf("完成的核验所有步骤都应完成：%+v", done.Steps)
		}
	}
}

func TestComplianceToolsAreIdempotentPerEvidenceKey(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	for i := 0; i < 3; i++ {
		if _, err := f.detect(t, complianceTestUser, created.ID, "target"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.detect(t, complianceTestUser, created.ID, "base-1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.compare(t, complianceTestUser, created.ID, 1, "base-1", 1); err != nil {
			t.Fatal(err)
		}
	}
	if f.engine.detects.Load() != 2 || f.engine.compares.Load() != 1 {
		t.Fatalf("同一个证据键只应调用一次引擎：detect=%d compare=%d", f.engine.detects.Load(), f.engine.compares.Load())
	}
	rows, _ := f.s.repo.ComplianceEvidenceForCheck(complianceTestUser, created.ID)
	if len(rows) != 3 {
		t.Fatalf("应只有 3 条证据：%d", len(rows))
	}
}

func TestComplianceCompareRequiresDetectedFaces(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	if _, err := f.compare(t, complianceTestUser, created.ID, 1, "base-1", 1); appErrorStatus(err) != 400 || !strings.Contains(err.Error(), "compliance_detect_faces") {
		t.Fatalf("没检测就比对应提示先检测：%v", err)
	}
	for _, image := range []string{"target", "base-1"} {
		if _, err := f.detect(t, complianceTestUser, created.ID, image); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.compare(t, complianceTestUser, created.ID, 5, "base-1", 1); appErrorStatus(err) != 400 {
		t.Fatalf("不存在的人脸序号应被拒绝：%v", err)
	}
	if _, err := f.compare(t, complianceTestUser, created.ID, 1, "base-2", 1); appErrorStatus(err) != 400 {
		t.Fatalf("不存在的底图应被拒绝：%v", err)
	}
	if _, err := f.detect(t, complianceTestUser, created.ID, "res_target"); appErrorStatus(err) != 400 {
		t.Fatalf("工具只认图片键，不接受资源 ID：%v", err)
	}
}

func TestComplianceSubmitRejectsIncompleteOrForgedReports(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	if _, err := f.submit(t, complianceTestUser, created.ID, validReport(1)); appErrorStatus(err) != 400 {
		t.Fatalf("没检测就提交应被拒绝：%v", err)
	}
	for _, image := range []string{"target", "base-1"} {
		if _, err := f.detect(t, complianceTestUser, created.ID, image); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.submit(t, complianceTestUser, created.ID, validReport(1)); appErrorStatus(err) != 400 || !strings.Contains(err.Error(), "还没有和授权底图比对") {
		t.Fatalf("有脸没比对时应被拒绝：%v", err)
	}
	// 相似度和人脸框不是 Agent 能填的字段：带上就是参数错误。
	forged := validReport(1)
	forged["similarity"] = 99
	if _, err := f.submit(t, complianceTestUser, created.ID, forged); err == nil {
		t.Fatal("报告里夹带相似度必须被拒绝")
	}
	if view, _ := f.s.ComplianceCheck(complianceTestUser, created.ID); view.Status != compliance.StatusRunning || view.Result != nil {
		t.Fatalf("被拒绝的提交不应结束核验：%+v", view)
	}
}

func TestComplianceSubmitWorksOnlyOnce(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	f.runFullFlow(t, created.ID)
	if _, err := f.submit(t, complianceTestUser, created.ID, validReport(1)); appErrorStatus(err) != 400 {
		t.Fatalf("第二次提交应被拒绝：%v", err)
	}
	if _, err := f.detect(t, complianceTestUser, created.ID, "target"); appErrorStatus(err) != 400 {
		t.Fatalf("核验结束后不能再调用比对工具：%v", err)
	}
}

func TestComplianceToolsOnlyTouchOwnBoundCheck(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	if _, err := f.detect(t, "someone-else", created.ID, "target"); appErrorStatus(err) != 404 {
		t.Fatalf("别人的核验应按不存在处理：%v", err)
	}
	if _, err := cloudAgentHandleComplianceDetectFaces(f.s.repo, complianceTestUser, &cloudAgentRuntime{}, complianceCall("compliance_detect_faces", map[string]any{"image": "target"}), f.s); appErrorStatus(err) != 400 {
		t.Fatalf("没有绑定核验时应报错：%v", err)
	}
	if _, err := cloudAgentHandleComplianceDetectFaces(f.s.repo, complianceTestUser, complianceState(created.ID), complianceCall("compliance_detect_faces", map[string]any{"image": "target", "checkId": "other"}), f.s); err == nil {
		t.Fatal("工具不接受 checkId，不能用参数改读别的核验")
	}
}

func TestComplianceEngineFailureIsReportedAndRetryable(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	f.engine.failNext.Store(true)
	if _, err := f.detect(t, complianceTestUser, created.ID, "target"); appErrorStatus(err) != 400 || strings.Contains(err.Error(), "引擎故障") {
		t.Fatalf("引擎失败应返回通用提示、不泄露内部原因：%v", err)
	}
	if _, err := f.detect(t, complianceTestUser, created.ID, "target"); err != nil {
		t.Fatalf("重试应成功：%v", err)
	}
}

func TestComplianceAgentEndingWithoutReportFailsTheCheck(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	if err := f.db.Model(&model.CloudAgentExecution{}).Where("id = ?", created.RunID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	view, err := f.s.ComplianceCheck(complianceTestUser, created.ID)
	if err != nil || view.Status != compliance.StatusFailed || view.Error == "" || view.Result != nil {
		t.Fatalf("Agent 已结束却没有提交结论，核验应收尾为失败：%+v (%v)", view, err)
	}
	if _, err := f.submit(t, complianceTestUser, created.ID, validReport(1)); appErrorStatus(err) != 400 {
		t.Fatalf("失败的核验不能再提交：%v", err)
	}
}

func TestComplianceSubmitBeatsLateReaping(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	done := f.runFullFlow(t, created.ID)
	if err := f.db.Model(&model.CloudAgentExecution{}).Where("id = ?", created.RunID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	view, err := f.s.ComplianceCheck(complianceTestUser, created.ID)
	if err != nil || view.Status != compliance.StatusDone || view.Result == nil || view.Result.Overall != done.Result.Overall {
		t.Fatalf("先提交、后结束的运行不能被收尾成失败：%+v (%v)", view, err)
	}
}

func TestStaleRunningComplianceCheckBecomesFailed(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	old := time.Now().UTC().Add(-2 * complianceStaleAfter)
	if err := f.db.Model(&model.ComplianceCheck{}).Where("id = ?", created.ID).Update("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	view, err := f.s.ComplianceCheck(complianceTestUser, created.ID)
	if err != nil || view.Status != compliance.StatusFailed || view.Error == "" {
		t.Fatalf("长时间没有进展的核验应收尾为失败：%+v (%v)", view, err)
	}
}

func TestComplianceProgressFollowsEvidence(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	states := func() string {
		view, err := f.s.ComplianceCheck(complianceTestUser, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, step := range view.Steps {
			out += step.State[:1]
		}
		return out
	}
	if got := states(); got != "appp" {
		t.Fatalf("刚开始第一步进行中：%s", got)
	}
	f.detect(t, complianceTestUser, created.ID, "target")
	f.detect(t, complianceTestUser, created.ID, "base-1")
	if got := states(); got != "adpp" {
		t.Fatalf("两张图都检测完：%s", got)
	}
	f.compare(t, complianceTestUser, created.ID, 1, "base-1", 1)
	if got := states(); got != "addp" {
		t.Fatalf("比对完成：%s", got)
	}
	if _, err := f.s.prepareComplianceImageInspection(complianceTestUser, complianceState(created.ID), complianceCall("compliance_inspect_images", map[string]any{"purpose": "判断画风"})); err != nil {
		t.Fatal(err)
	}
	if got := states(); got != "ddda" {
		t.Fatalf("看过图后只剩生成结论：%s", got)
	}
	var check model.ComplianceCheck
	if err := f.db.First(&check, "id = ?", created.ID).Error; err != nil || check.StepsDone != 3 {
		t.Fatalf("进度应写进核验记录：%+v (%v)", check, err)
	}
}

func TestComplianceInspectImagesAttachesOnlyOwnCheckImages(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t, "res_base1", "res_base2")
	state := complianceState(created.ID)
	result, err := f.s.prepareComplianceImageInspection(complianceTestUser, state, complianceCall("compliance_inspect_images", map[string]any{"purpose": "判断画风与人脸数量"}))
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := result.(cloudAgentImageInspectionBatch)
	if !ok || len(batch.Inspections) != 3 {
		t.Fatalf("省略 images 应看全部三张图：%+v", result)
	}
	wantURLs := []string{"resource:res_target", "resource:res_base1", "resource:res_base2"}
	wantKeys := []string{"target", "base-1", "base-2"}
	for i, inspection := range batch.Inspections {
		if inspection.ImageURL != wantURLs[i] || inspection.Receipt["nodeId"] != wantKeys[i] {
			t.Fatalf("第 %d 张图应是账号资源引用并以图片键作 nodeId：%+v", i, inspection)
		}
	}
	parts := cloudAgentImageContentParts(batch.Inspections...)
	canonical := canonicalAgentRequest{ToolChoice: "auto", Messages: []map[string]any{{"role": "user", "content": parts}}}
	refs, err := f.s.cloudAgentImageReferences(complianceTestUser, state.Request, &canonical)
	if err != nil || len(refs) != 3 {
		t.Fatalf("看图结果应能被现有管线水合成获准的图片清单：%+v (%v)", refs, err)
	}

	for name, args := range map[string]map[string]any{
		"没有 purpose": {"images": []string{"target"}},
		"不存在的底图":     {"purpose": "x", "images": []string{"base-3"}},
		"重复":         {"purpose": "x", "images": []string{"target", "target"}},
		"资源 ID":      {"purpose": "x", "images": []string{"res_target"}},
	} {
		if _, err := f.s.prepareComplianceImageInspection(complianceTestUser, complianceState(created.ID), complianceCall("compliance_inspect_images", args)); appErrorStatus(err) != 400 {
			t.Errorf("%s：应返回 400，得到 %v", name, err)
		}
	}
	if _, err := f.s.prepareComplianceImageInspection("someone-else", complianceState(created.ID), complianceCall("compliance_inspect_images", map[string]any{"purpose": "x"})); appErrorStatus(err) != 404 {
		t.Fatalf("别人的核验应按不存在处理：%v", err)
	}
}

func TestComplianceInspectImagesRespectsRunImageBudget(t *testing.T) {
	f := newComplianceFixture(t)
	created := f.create(t)
	state := complianceState(created.ID)
	state.ImageInspectCalls = cloudAgentMaxImageInspectionCallsPerRun
	if _, err := f.s.prepareComplianceImageInspection(complianceTestUser, state, complianceCall("compliance_inspect_images", map[string]any{"purpose": "x"})); !errors.Is(err, errCloudAgentImageInspectionBudget) {
		t.Fatalf("超出本轮图片预算应返回预算错误：%v", err)
	}
}

func TestComplianceImagesBlockResourceDeletion(t *testing.T) {
	f := newComplianceFixture(t)
	f.create(t)
	refs, err := f.s.repo.ComplianceResourceReferences(complianceTestUser, []string{"res_target", "res_unused"})
	if err != nil || len(refs) != 1 || refs[0].ResourceID != "res_target" || refs[0].Kind != "合规核验" {
		t.Fatalf("核验用到的图片应被登记为引用：%+v (%v)", refs, err)
	}
	if refs, _ := f.s.repo.ComplianceResourceReferences("someone-else", []string{"res_target"}); len(refs) != 0 {
		t.Fatalf("别人的资源引用不应被查到：%+v", refs)
	}
}
