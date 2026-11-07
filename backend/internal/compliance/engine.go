package compliance

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"
)

// ImageRef 指向用户已上传的资源；引擎自己按 ResourceID 取图，调用方保证资源归属当前用户。
type ImageRef struct {
	ResourceID string
	Name       string
}

// FaceRef 指向某张图里的第 Index 张脸（从 1 开始，与 DetectedFace.Index 一致）。
type FaceRef struct {
	Image ImageRef
	Index int
}

// DetectedFace 是检测引擎在一张图里找到的一张脸。Quality 为 0-100，越高越适合比对。
type DetectedFace struct {
	Index   int     `json:"index"`
	Box     FaceBox `json:"box"`
	Quality int     `json:"quality"`
}

// FaceEngine 是比对引擎的适配层，由 Agent 的工具逐步调用。Compare 返回 0-100 的相似度。
// 引擎不得把人脸图或特征向量写进日志。
type FaceEngine interface {
	Detect(ctx context.Context, image ImageRef) ([]DetectedFace, error)
	Compare(ctx context.Context, a, b FaceRef) (int, error)
}

// MockFaceEngine 按资源 ID 生成固定的演示结果，没有接入真实引擎前使用。它只能验证 Agent 链路，
// 相似度不反映真实人脸，不能当作识别能力交付。
type MockFaceEngine struct {
	// StepDelay 是每次调用的模拟耗时；测试里设为 0。
	StepDelay time.Duration
}

var mockFaceBoxes = []FaceBox{
	{X: 14, Y: 14, W: 30, H: 42},
	{X: 56, Y: 22, W: 28, H: 38},
}

func mockSeed(parts ...string) int {
	hash := fnv.New32a()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part + "|"))
	}
	return int(hash.Sum32() >> 1)
}

func (m MockFaceEngine) Detect(ctx context.Context, image ImageRef) ([]DetectedFace, error) {
	if err := sleep(ctx, m.StepDelay); err != nil {
		return nil, err
	}
	seed := mockSeed(image.ResourceID)
	count := 1
	if seed%3 == 0 {
		count = 2
	}
	faces := make([]DetectedFace, 0, count)
	for i := 0; i < count; i++ {
		faces = append(faces, DetectedFace{Index: i + 1, Box: mockFaceBoxes[i], Quality: 60 + (seed>>(i+2))%40})
	}
	return faces, nil
}

func (m MockFaceEngine) Compare(ctx context.Context, a, b FaceRef) (int, error) {
	if err := sleep(ctx, m.StepDelay); err != nil {
		return 0, err
	}
	return 38 + mockSeed(a.Image.ResourceID, fmt.Sprint(a.Index), b.Image.ResourceID, fmt.Sprint(b.Index))%60, nil
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
