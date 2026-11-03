// Package compliance 是合规核验的领域规则：人脸比对的结果类型、判定阈值和比对引擎接口。
// 判定只来自引擎给出的相似度与这里的阈值，AI（Agent）只读取并解读结果，不参与判定。
// 本包不依赖 internal/app 与 internal/service。
package compliance

import (
	"context"
	"errors"
	"hash/fnv"
	"time"
)

const (
	KindFaceCompare = "face_compare"

	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"

	// StepCount 是人脸比对的步骤数：检测人脸、比对底图、生成结论。
	StepCount = 3

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

type FaceCompareResult struct {
	Overall OverallVerdict `json:"overall"`
	Faces   []FaceItem     `json:"faces"`
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

// ErrNoBaseImage 表示比对时没有授权底图。
var ErrNoBaseImage = errors.New("至少需要一张授权底图")

// ImageRef 指向用户已上传的资源；引擎自己按 ResourceID 取图，调用方保证资源归属当前用户。
type ImageRef struct {
	ResourceID string
	Name       string
}

// FaceComparer 是比对引擎的适配层。progress 在每个阶段完成后以已完成步数调用
// （最后一步由调用方在保存结果时收尾）。引擎不得把人脸图或特征向量写进日志。
type FaceComparer interface {
	Compare(ctx context.Context, target ImageRef, bases []ImageRef, progress func(stepsDone int)) (FaceCompareResult, error)
}

// MockFaceComparer 按资源 ID 生成固定的演示结果，没有接入真实引擎前使用。
type MockFaceComparer struct {
	// StepDelay 是每个阶段的模拟耗时；测试里设为 0。
	StepDelay time.Duration
}

var mockFaceBoxes = []FaceBox{
	{X: 14, Y: 14, W: 30, H: 42},
	{X: 56, Y: 22, W: 28, H: 38},
}

func (m MockFaceComparer) Compare(ctx context.Context, target ImageRef, bases []ImageRef, progress func(stepsDone int)) (FaceCompareResult, error) {
	if len(bases) == 0 {
		return FaceCompareResult{}, ErrNoBaseImage
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(target.ResourceID))
	for _, base := range bases {
		_, _ = hash.Write([]byte("|" + base.ResourceID))
	}
	seed := int(hash.Sum32() >> 1)

	for step := 1; step < StepCount; step++ {
		if err := sleep(ctx, m.StepDelay); err != nil {
			return FaceCompareResult{}, err
		}
		progress(step)
	}

	count := 1
	if seed%3 == 0 {
		count = 2
	}
	faces := make([]FaceItem, 0, count)
	for i := 0; i < count; i++ {
		similarity := 60 + seed%38
		if i > 0 {
			similarity = 38 + (seed>>3)%30
		}
		faces = append(faces, FaceItem{Index: i + 1, Box: mockFaceBoxes[i], Similarity: similarity, BestBaseIndex: (seed + i) % len(bases), Verdict: VerdictOf(similarity)})
	}
	return FaceCompareResult{Overall: OverallOf(faces), Faces: faces}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	// select 在取消和定时器同时就绪时随机选分支，先判断取消才不会漏掉。
	if err := ctx.Err(); err != nil || d <= 0 {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
