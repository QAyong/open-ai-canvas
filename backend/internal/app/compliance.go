// 合规核验：创建核验、后台运行比对、读取结果。Agent 的 compliance_get_check 与页面共用这里的读取，
// 归属校验也只在这里做。比对判定见 internal/compliance，引擎目前是 mock 适配层。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

const (
	complianceRunTimeout = 2 * time.Minute
	// 超过这个时间仍在运行的核验多半是服务重启时丢了后台任务，读取时收尾为失败。
	complianceStaleAfter  = 5 * time.Minute
	compliancePromptLimit = 2000
	complianceNameLimit   = 128
	complianceFileLimit   = 256
	// mock 引擎每个阶段的模拟耗时，页面据此逐步展示进度。
	complianceMockStepDelay = 900 * time.Millisecond
)

type ComplianceImageInput struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
}

type CreateComplianceCheckRequest struct {
	Kind        string                 `json:"kind"`
	Prompt      string                 `json:"prompt"`
	SubjectName string                 `json:"subjectName"`
	Target      ComplianceImageInput   `json:"target"`
	Bases       []ComplianceImageInput `json:"bases"`
}

type ComplianceThresholds struct {
	Match   int `json:"match"`
	Suspect int `json:"suspect"`
}

type ComplianceCheckView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Prompt      string `json:"prompt"`
	SubjectName string `json:"subjectName"`
	Status      string `json:"status"`
	StepsDone   int    `json:"stepsDone"`
	StepCount   int    `json:"stepCount"`
	// Thresholds 是判定一致/疑似的相似度阈值，结果页的规则说明据此生成，前端不写死。
	Thresholds ComplianceThresholds          `json:"thresholds"`
	Target     ComplianceImageInput          `json:"target"`
	Bases      []ComplianceImageInput        `json:"bases"`
	Result     *compliance.FaceCompareResult `json:"result,omitempty"`
	Error      string                        `json:"error,omitempty"`
	CreatedAt  time.Time                     `json:"createdAt"`
	UpdatedAt  time.Time                     `json:"updatedAt"`
}

func (s *Service) faceComparer() compliance.FaceComparer {
	if s.complianceComparer != nil {
		return s.complianceComparer
	}
	return compliance.MockFaceComparer{StepDelay: complianceMockStepDelay}
}

func (s *Service) CreateComplianceCheck(actor *model.User, req CreateComplianceCheckRequest) (*ComplianceCheckView, error) {
	if actor == nil {
		return nil, kernel.Unauthorized("请先登录")
	}
	if req.Kind != compliance.KindFaceCompare {
		return nil, kernel.BadAuthRequest("暂不支持这种核验类型")
	}
	prompt, subject := strings.TrimSpace(req.Prompt), strings.TrimSpace(req.SubjectName)
	if len([]rune(prompt)) > compliancePromptLimit {
		return nil, kernel.BadAuthRequest("核验说明过长")
	}
	if len([]rune(subject)) > complianceNameLimit {
		return nil, kernel.BadAuthRequest("授权人姓名过长")
	}
	if len(req.Bases) == 0 || len(req.Bases) > compliance.MaxBaseImages {
		return nil, kernel.BadAuthRequest("授权底图需要 1 到 3 张")
	}

	now := time.Now().UTC()
	check := &model.ComplianceCheck{
		ID: "chk_" + kernel.NewID(), UserID: actor.ID, Kind: req.Kind, Prompt: prompt, SubjectName: subject,
		Status: compliance.StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	var images []model.ComplianceCheckImage
	seen := map[string]bool{}
	addImage := func(role string, position int, input ComplianceImageInput) error {
		resourceID := strings.TrimSpace(input.ResourceID)
		if resourceID == "" {
			return kernel.BadAuthRequest("缺少图片资源")
		}
		if seen[role+resourceID] {
			return kernel.BadAuthRequest("同一张图片不能重复使用")
		}
		seen[role+resourceID] = true
		// 所有图片都必须是当前用户已上传完成的图片资源，不信任客户端传来的 ID。
		resource, err := s.repo.ResourceForUser(actor.ID, resourceID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return kernel.BadAuthRequest("图片资源不存在")
			}
			return err
		}
		if resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
			return kernel.BadAuthRequest("只能使用已上传完成的图片")
		}
		name := strings.TrimSpace(input.Name)
		if len([]rune(name)) > complianceFileLimit {
			name = string([]rune(name)[:complianceFileLimit])
		}
		images = append(images, model.ComplianceCheckImage{ID: "chi_" + kernel.NewID(), CheckID: check.ID, UserID: actor.ID, Role: role, Position: position, ResourceID: resourceID, Name: name, CreatedAt: now})
		return nil
	}
	if err := addImage(model.ComplianceImageTarget, 0, req.Target); err != nil {
		return nil, err
	}
	for i, base := range req.Bases {
		if err := addImage(model.ComplianceImageBase, i, base); err != nil {
			return nil, err
		}
	}
	if err := s.repo.CreateComplianceCheck(check, images); err != nil {
		return nil, err
	}

	target := compliance.ImageRef{ResourceID: images[0].ResourceID, Name: images[0].Name}
	bases := make([]compliance.ImageRef, 0, len(images)-1)
	for _, image := range images[1:] {
		bases = append(bases, compliance.ImageRef{ResourceID: image.ResourceID, Name: image.Name})
	}
	go s.runComplianceCheck(check.ID, target, bases)
	return s.ComplianceCheck(actor.ID, check.ID)
}

// runComplianceCheck 在后台调用比对引擎并写回进度与结果。失败原因只写日志，页面只看到通用提示。
func (s *Service) runComplianceCheck(checkID string, target compliance.ImageRef, bases []compliance.ImageRef) {
	ctx, cancel := context.WithTimeout(context.Background(), complianceRunTimeout)
	defer cancel()
	result, err := s.faceComparer().Compare(ctx, target, bases, func(stepsDone int) {
		if err := s.repo.UpdateComplianceCheckProgress(checkID, stepsDone); err != nil {
			slog.Warn("更新合规核验进度失败", "checkId", checkID, "error", err)
		}
	})
	if err == nil {
		var encoded []byte
		if encoded, err = json.Marshal(result); err == nil {
			err = s.repo.FinishComplianceCheck(checkID, compliance.StatusDone, compliance.StepCount, string(encoded), "")
		}
	}
	if err != nil {
		slog.Warn("合规核验失败", "checkId", checkID, "error", err)
		if finishErr := s.repo.FinishComplianceCheck(checkID, compliance.StatusFailed, 0, "", "核验失败，请稍后重试"); finishErr != nil {
			slog.Warn("合规核验收尾失败", "checkId", checkID, "error", finishErr)
		}
	}
}

// ComplianceCheck 返回属于该用户的核验；不是自己的核验一律按不存在处理。
func (s *Service) ComplianceCheck(userID, id string) (*ComplianceCheckView, error) {
	check, images, err := s.repo.ComplianceCheckForUser(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("核验不存在")
		}
		return nil, err
	}
	if check.Status == compliance.StatusRunning && time.Since(check.UpdatedAt) > complianceStaleAfter {
		if err := s.repo.FinishComplianceCheck(check.ID, compliance.StatusFailed, check.StepsDone, "", "核验超时或服务已重启，请重新发起"); err != nil {
			return nil, err
		}
		if check, images, err = s.repo.ComplianceCheckForUser(userID, id); err != nil {
			return nil, err
		}
	}
	view := &ComplianceCheckView{
		ID: check.ID, Kind: check.Kind, Prompt: check.Prompt, SubjectName: check.SubjectName, Status: check.Status,
		StepsDone: check.StepsDone, StepCount: compliance.StepCount, Thresholds: ComplianceThresholds{Match: compliance.MatchThreshold, Suspect: compliance.SuspectThreshold}, Bases: []ComplianceImageInput{}, Error: check.Error,
		CreatedAt: check.CreatedAt, UpdatedAt: check.UpdatedAt,
	}
	for _, image := range images {
		input := ComplianceImageInput{ResourceID: image.ResourceID, Name: image.Name}
		if image.Role == model.ComplianceImageTarget {
			view.Target = input
		} else {
			view.Bases = append(view.Bases, input)
		}
	}
	if check.ResultJSON != "" {
		var result compliance.FaceCompareResult
		if err := json.Unmarshal([]byte(check.ResultJSON), &result); err != nil {
			return nil, err
		}
		view.Result = &result
	}
	return view, nil
}

// complianceAgentWait 是 Agent 读取时最多等待运行中的核验完成的时间：页面提交后 Agent 会立刻启动，
// 常常早于比对结束；有界等待让它一次读到结果，而不是读到"进行中"后反复轮询。
var complianceAgentWait = 8 * time.Second

const complianceAgentPoll = 250 * time.Millisecond

const complianceAgentNote = "结论只来自比对引擎的相似度与固定阈值，不得改判或编造相似度；图片内容不提供，不要描述图片；状态以本次读取为准"

// cloudAgentHandleComplianceGetCheck 是 Agent 读取本轮绑定核验的只读工具。
// 它只能读本轮绑定的那一次核验，归属校验沿用页面读取的同一个服务方法。
func cloudAgentHandleComplianceGetCheck(_ *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall, services ...*Service) (any, error) {
	if len(services) == 0 || services[0] == nil {
		return nil, BadAuthRequest("合规服务不可用")
	}
	var args struct{}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if state.Request.ComplianceCheckID == "" {
		return nil, BadAuthRequest("本轮没有绑定合规核验")
	}
	readAt := time.Now()
	view, err := services[0].ComplianceCheck(userID, state.Request.ComplianceCheckID)
	for deadline := readAt.Add(complianceAgentWait); err == nil && view.Status == compliance.StatusRunning && time.Now().Before(deadline); {
		time.Sleep(complianceAgentPoll)
		view, err = services[0].ComplianceCheck(userID, state.Request.ComplianceCheckID)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"source": "compliance_db", "readAt": readAt.Format(time.RFC3339), "note": complianceAgentNote,
		"check": view,
		"verdicts": map[string]string{
			"match": "相似度达到一致阈值", "suspect": "介于疑似与一致阈值之间，建议人工确认", "mismatch": "低于疑似阈值",
			"pass": "全部人脸都与授权人一致", "review": "存在疑似，或有人脸未能匹配授权人", "risk": "没有任何人脸与授权人一致", "no_face": "没有检出人脸",
		},
	}, nil
}
