package compliance

import (
	"context"
	"errors"
	"testing"
)

func TestVerdictOfUsesThresholds(t *testing.T) {
	cases := map[int]FaceVerdict{MatchThreshold: FaceMatch, MatchThreshold - 1: FaceSuspect, SuspectThreshold: FaceSuspect, SuspectThreshold - 1: FaceMismatch, 0: FaceMismatch, 100: FaceMatch}
	for similarity, want := range cases {
		if got := VerdictOf(similarity); got != want {
			t.Errorf("VerdictOf(%d) = %s, want %s", similarity, got, want)
		}
	}
}

func TestOverallOfSendsExtraFacesToReview(t *testing.T) {
	face := func(similarity int) FaceItem { return FaceItem{Similarity: similarity, Verdict: VerdictOf(similarity)} }
	cases := []struct {
		name  string
		faces []FaceItem
		want  OverallVerdict
	}{
		{"没有人脸", nil, OverallNoFace},
		{"唯一的人脸一致", []FaceItem{face(95)}, OverallPass},
		{"多出一张不一致的人脸", []FaceItem{face(95), face(40)}, OverallReview},
		{"只有疑似", []FaceItem{face(75)}, OverallReview},
		{"全部不一致", []FaceItem{face(40), face(30)}, OverallRisk},
	}
	for _, tc := range cases {
		if got := OverallOf(tc.faces); got != tc.want {
			t.Errorf("%s: OverallOf = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestMockFaceComparerIsDeterministicAndReportsProgress(t *testing.T) {
	target := ImageRef{ResourceID: "res_target"}
	bases := []ImageRef{{ResourceID: "res_a"}, {ResourceID: "res_b"}}
	var steps []int
	first, err := MockFaceComparer{}.Compare(context.Background(), target, bases, func(done int) { steps = append(steps, done) })
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != StepCount-1 || steps[0] != 1 || steps[len(steps)-1] != StepCount-1 {
		t.Fatalf("progress = %v，期望 1..%d", steps, StepCount-1)
	}
	second, err := MockFaceComparer{}.Compare(context.Background(), target, bases, func(int) {})
	if err != nil || len(first.Faces) != len(second.Faces) || first.Overall != second.Overall || first.Faces[0].Similarity != second.Faces[0].Similarity {
		t.Fatalf("同一组资源应得到同一结果：%+v vs %+v (%v)", first, second, err)
	}
	for _, face := range first.Faces {
		if face.BestBaseIndex < 0 || face.BestBaseIndex >= len(bases) || face.Verdict != VerdictOf(face.Similarity) {
			t.Fatalf("人脸结果不自洽：%+v", face)
		}
	}
}

func TestMockFaceComparerRejectsMissingBasesAndCancellation(t *testing.T) {
	if _, err := (MockFaceComparer{}).Compare(context.Background(), ImageRef{ResourceID: "t"}, nil, func(int) {}); !errors.Is(err, ErrNoBaseImage) {
		t.Fatalf("没有底图应返回 ErrNoBaseImage，得到 %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (MockFaceComparer{StepDelay: 1}).Compare(ctx, ImageRef{ResourceID: "t"}, []ImageRef{{ResourceID: "b"}}, func(int) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的上下文应返回 context.Canceled，得到 %v", err)
	}
}
