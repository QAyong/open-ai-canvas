// Package compliance 是合规核验的领域规则：人脸比对的结果类型、判定阈值、比对引擎接口和 Agent 报告校验。
// 事实（人脸框、相似度）只来自引擎，判定只来自这里的阈值；Agent 按技能编排工具并写下依据与建议，
// 服务端用 BuildReport 把它的陈述和引擎证据对齐后才落库。本包不依赖 internal/app 与 internal/service。
package compliance

const (
	KindFaceCompare = "face_compare"

	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"

	// MatchThreshold 及以上视为一致；介于两个阈值之间为疑似。相似度为 0-100 的整数。
	MatchThreshold   = 85
	SuspectThreshold = 70

	// MaxBaseImages 是单次核验最多使用的授权底图数。
	MaxBaseImages = 3
)

type FaceVerdict string

const (
	FaceMatch    FaceVerdict = "match"
	FaceSuspect  FaceVerdict = "suspect"
	FaceMismatch FaceVerdict = "mismatch"
)

type OverallVerdict string

const (
	OverallPass   OverallVerdict = "pass"
	OverallReview OverallVerdict = "review"
	OverallRisk   OverallVerdict = "risk"
	OverallNoFace OverallVerdict = "no_face"
)

// FaceBox 用占图片宽高的百分比表示，和图片实际像素无关。
type FaceBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type FaceItem struct {
	// Index 从 1 开始，对应结果页里的人脸编号。
	Index int     `json:"index"`
	Box   FaceBox `json:"box"`
	// Similarity 是与最接近的授权底图的相似度。
	Similarity    int         `json:"similarity"`
	BestBaseIndex int         `json:"bestBaseIndex"`
	Verdict       FaceVerdict `json:"verdict"`
}

func VerdictOf(similarity int) FaceVerdict {
	switch {
	case similarity >= MatchThreshold:
		return FaceMatch
	case similarity >= SuspectThreshold:
		return FaceSuspect
	default:
		return FaceMismatch
	}
}

// OverallOf 在全部人脸都与授权人一致时才通过；多出未匹配的人脸或存在"疑似"都转人工复核。
func OverallOf(faces []FaceItem) OverallVerdict {
	if len(faces) == 0 {
		return OverallNoFace
	}
	matched, suspect := 0, false
	for _, face := range faces {
		switch face.Verdict {
		case FaceMatch:
			matched++
		case FaceSuspect:
			suspect = true
		}
	}
	switch {
	case matched == len(faces):
		return OverallPass
	case matched > 0 || suspect:
		return OverallReview
	default:
		return OverallRisk
	}
}

// severity 把整体结论排成可比较的风险高低；no_face 不参与升级。
func severity(overall OverallVerdict) int {
	switch overall {
	case OverallReview:
		return 1
	case OverallRisk:
		return 2
	default:
		return 0
	}
}
