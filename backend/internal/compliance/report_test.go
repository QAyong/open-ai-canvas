package compliance

import (
	"strings"
	"testing"
)

func evidence(similarities ...int) Evidence {
	e := Evidence{BaseCount: 1, Inspected: true, Detect: map[string][]DetectedFace{
		ImageTarget: {},
		BaseKey(0):  {{Index: 1, Box: FaceBox{X: 10, Y: 10, W: 30, H: 40}, Quality: 90}},
	}}
	for i, similarity := range similarities {
		e.Detect[ImageTarget] = append(e.Detect[ImageTarget], DetectedFace{Index: i + 1, Box: FaceBox{X: float64(i * 40), Y: 5, W: 30, H: 40}, Quality: 80})
		e.Compares = append(e.Compares, CompareFact{TargetFace: i + 1, Base: BaseKey(0), BaseFace: 1, Similarity: similarity})
	}
	return e
}

func submit(faces ...int) SubmitInput {
	in := SubmitInput{Summary: "已完成比对。", Style: StyleRealistic}
	for _, index := range faces {
		in.Faces = append(in.Faces, FaceNote{Index: index, Basis: []string{"脸型与五官布局相近"}})
	}
	return in
}

func TestBuildReportDerivesNumbersAndVerdictsFromEvidence(t *testing.T) {
	report, err := BuildReport(evidence(91), submit(1))
	if err != nil {
		t.Fatal(err)
	}
	face := report.Faces[0]
	if report.Overall != OverallPass || face.Similarity != 91 || face.Verdict != FaceMatch || face.Box.W != 30 || report.HumanReview.Required || report.Disclaimer == "" {
		t.Fatalf("数字、人脸框和结论都应来自证据与阈值：%+v", report)
	}
	if report.Suggestions == nil || face.Limitations == nil {
		t.Fatalf("空列表应序列化为 []，不能是 null：%+v", report)
	}
}

func TestBuildReportTakesBestBaseAcrossComparisons(t *testing.T) {
	e := evidence(60)
	e.BaseCount = 2
	e.Detect[BaseKey(1)] = []DetectedFace{{Index: 1}}
	e.Compares = append(e.Compares, CompareFact{TargetFace: 1, Base: BaseKey(1), BaseFace: 1, Similarity: 88})
	report, err := BuildReport(e, submit(1))
	if err != nil {
		t.Fatal(err)
	}
	if face := report.Faces[0]; face.Similarity != 88 || face.BestBaseIndex != 1 {
		t.Fatalf("应取与各底图比对的最高相似度及其底图位置：%+v", face)
	}
}

func TestBuildReportRequiresEveryFaceComparedAndNoted(t *testing.T) {
	e := evidence(90, 50)
	e.Compares = e.Compares[:1]
	if _, err := BuildReport(e, submit(1, 2)); err == nil || !strings.Contains(err.Error(), "第 2 张脸还没有和授权底图比对") {
		t.Fatalf("有脸没比对时必须拒绝：%v", err)
	}
	if _, err := BuildReport(evidence(90, 50), submit(1)); err == nil || !strings.Contains(err.Error(), "缺少第 2 张脸的判断") {
		t.Fatalf("有脸没写判断时必须拒绝：%v", err)
	}
	if _, err := BuildReport(evidence(90), submit(1, 1)); err == nil || !strings.Contains(err.Error(), "不止一次") {
		t.Fatalf("同一张脸写两次必须拒绝：%v", err)
	}
	if _, err := BuildReport(evidence(90), submit(1, 7)); err == nil || !strings.Contains(err.Error(), "没有检出第 7 张脸") {
		t.Fatalf("编造不存在的脸必须拒绝：%v", err)
	}
	if _, err := BuildReport(Evidence{Detect: map[string][]DetectedFace{}}, submit()); err == nil {
		t.Fatal("没有检测待核验图时必须拒绝")
	}
}

func TestBuildReportNoFace(t *testing.T) {
	e := Evidence{BaseCount: 1, Detect: map[string][]DetectedFace{ImageTarget: {}, BaseKey(0): {{Index: 1}}}}
	report, err := BuildReport(e, submit())
	if err != nil || report.Overall != OverallNoFace || len(report.Faces) != 0 || !report.HumanReview.Required {
		t.Fatalf("没检出人脸应是 no_face 并转人工：%+v (%v)", report, err)
	}
	in := submit()
	in.EscalateTo, in.EscalateReason = OverallRisk, "图片可疑"
	if report, _ = BuildReport(e, in); report.Overall != OverallNoFace || report.Escalation != nil {
		t.Fatalf("no_face 不参与升级：%+v", report)
	}
}

func TestBuildReportStylizedCannotPass(t *testing.T) {
	in := submit(1)
	in.Style = StyleStylized
	report, err := BuildReport(evidence(95), in)
	if err != nil || report.Overall != OverallReview || !report.HumanReview.Required || !strings.Contains(report.HumanReview.Reason, "风格化") {
		t.Fatalf("风格化图不能仅凭相似度通过：%+v (%v)", report, err)
	}
	if report.Faces[0].Verdict != FaceMatch {
		t.Fatalf("单张脸的结论仍按阈值：%+v", report.Faces[0])
	}
}

func TestBuildReportEscalationOnlyRaises(t *testing.T) {
	in := submit(1)
	in.EscalateTo = OverallReview
	if _, err := BuildReport(evidence(95), in); err == nil || !strings.Contains(err.Error(), "escalateReason") {
		t.Fatalf("升级必须写理由：%v", err)
	}
	in.EscalateReason = "遮挡严重，需要人工确认"
	report, err := BuildReport(evidence(95), in)
	if err != nil || report.Overall != OverallReview || report.Escalation == nil || report.Escalation.To != OverallReview {
		t.Fatalf("Agent 可以把通过升为复核：%+v (%v)", report, err)
	}
	in.EscalateTo = OverallPass
	if _, err := BuildReport(evidence(95), in); err == nil {
		t.Fatal("escalateTo 不能是 pass")
	}
	in.EscalateTo = OverallReview
	report, err = BuildReport(evidence(40), in)
	if err != nil || report.Overall != OverallRisk || report.Escalation != nil {
		t.Fatalf("升级不能把 risk 压成 review：%+v (%v)", report, err)
	}
}

func TestBuildReportRejectsConclusiveWording(t *testing.T) {
	for _, phrase := range []string{"可以确认侵权", "确认同一人", "该素材绝对安全", "可以放心商用"} {
		in := submit(1)
		in.Summary = phrase
		if _, err := BuildReport(evidence(95), in); err == nil || !strings.Contains(err.Error(), "结论式措辞") {
			t.Errorf("%q 应被拒绝：%v", phrase, err)
		}
	}
	in := submit(1)
	in.Faces[0].Basis = []string{"相似度较高，但不能据此确认侵权"}
	if _, err := BuildReport(evidence(95), in); err != nil {
		t.Fatalf("带否定的边界说明应放行：%v", err)
	}
	in = submit(1)
	in.Suggestions = []string{"这个形象未侵权"}
	if _, err := BuildReport(evidence(95), in); err == nil {
		t.Fatal("建议里的结论式措辞同样要拒绝")
	}
}

func TestBuildReportValidatesShape(t *testing.T) {
	in := submit(1)
	in.Style = "cartoon"
	if _, err := BuildReport(evidence(90), in); err == nil {
		t.Fatal("未知画风必须拒绝")
	}
	in = submit(1)
	in.Summary = " "
	if _, err := BuildReport(evidence(90), in); err == nil {
		t.Fatal("summary 不能为空")
	}
	in = submit(1)
	in.Faces[0].Basis = nil
	if _, err := BuildReport(evidence(90), in); err == nil {
		t.Fatal("每张脸至少一条依据")
	}
	in = submit(1)
	in.Suggestions = make([]string, maxItems+1)
	for i := range in.Suggestions {
		in.Suggestions[i] = "调整发型"
	}
	if _, err := BuildReport(evidence(90), in); err == nil {
		t.Fatal("建议条数要有上限")
	}
}

func TestStepsFollowEvidence(t *testing.T) {
	state := func(steps []Step) string {
		out := ""
		for _, step := range steps {
			out += step.State[:1]
		}
		return out
	}
	e := Evidence{BaseCount: 1, Detect: map[string][]DetectedFace{}}
	if got := state(Steps(e, StatusRunning)); got != "appp" {
		t.Fatalf("还没做任何事时第一步进行中：%s", got)
	}
	e.Inspected = true
	e.Detect[ImageTarget] = []DetectedFace{{Index: 1}}
	if got := state(Steps(e, StatusRunning)); got != "dapp" {
		t.Fatalf("只检测了待核验图时检测仍在进行：%s", got)
	}
	e.Detect[BaseKey(0)] = []DetectedFace{{Index: 1}}
	e.Compares = []CompareFact{{TargetFace: 1, Base: BaseKey(0), BaseFace: 1, Similarity: 90}}
	if got := state(Steps(e, StatusRunning)); got != "ddda" {
		t.Fatalf("三步完成后生成结论进行中：%s", got)
	}
	if got := StepsDone(e, StatusDone); got != StepCount {
		t.Fatalf("核验完成后全部完成：%d", got)
	}
	if got := state(Steps(Evidence{BaseCount: 1}, StatusFailed)); got != "pppp" {
		t.Fatalf("失败后不再有进行中的步骤：%s", got)
	}
}

func TestBasePositionRoundTrips(t *testing.T) {
	for i := 0; i < MaxBaseImages; i++ {
		if position, ok := BasePosition(BaseKey(i)); !ok || position != i {
			t.Fatalf("BasePosition(%q) = %d, %v", BaseKey(i), position, ok)
		}
	}
	for _, key := range []string{"target", "base-0", "base-4", "base-01", "base-1x", ""} {
		if _, ok := BasePosition(key); ok {
			t.Errorf("%q 不应被识别为底图键", key)
		}
	}
}
