package compliance

import (
	"context"
	"errors"
	"testing"
)

func TestMockFaceEngineIsDeterministic(t *testing.T) {
	engine := MockFaceEngine{}
	image := ImageRef{ResourceID: "res_target"}
	first, err := engine.Detect(context.Background(), image)
	if err != nil || len(first) == 0 {
		t.Fatalf("检测应至少返回一张脸：%+v (%v)", first, err)
	}
	second, _ := engine.Detect(context.Background(), image)
	if len(first) != len(second) || first[0] != second[0] {
		t.Fatalf("同一张图应得到同一结果：%+v vs %+v", first, second)
	}
	for i, face := range first {
		if face.Index != i+1 || face.Quality < 0 || face.Quality > 100 {
			t.Fatalf("人脸序号应从 1 连续，质量应在 0-100：%+v", face)
		}
	}
	a, b := FaceRef{Image: image, Index: 1}, FaceRef{Image: ImageRef{ResourceID: "res_base"}, Index: 1}
	one, err := engine.Compare(context.Background(), a, b)
	two, _ := engine.Compare(context.Background(), a, b)
	if err != nil || one != two || one < 0 || one > 100 {
		t.Fatalf("比对应得到固定的 0-100 相似度：%d / %d (%v)", one, two, err)
	}
}

func TestMockFaceEngineHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := MockFaceEngine{StepDelay: 1}
	if _, err := engine.Detect(ctx, ImageRef{ResourceID: "t"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的上下文应返回 context.Canceled，得到 %v", err)
	}
	if _, err := engine.Compare(ctx, FaceRef{}, FaceRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的上下文应返回 context.Canceled，得到 %v", err)
	}
}
