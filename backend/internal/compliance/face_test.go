package compliance

import "testing"

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
