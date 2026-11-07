// 合规核验：创建核验并由服务端发起 Agent 运行、读取结果。核验由 Agent 按合规技能调用比对工具完成
// （见 compliance_agent.go）；归属校验只在这里做，页面与 Agent 的 compliance_get_check 共用同一个读取。
// 判定规则与报告校验见 internal/compliance，引擎目前是 mock 适配层。
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	// 超过这个时间没有任何进展的核验多半是服务重启时丢了 Agent 运行，读取时收尾为失败。
	complianceStaleAfter  = 10 * time.Minute
	compliancePromptLimit = 2000
	complianceNameLimit   = 128
	complianceFileLimit   = 256

	// 一次核验的 Agent 运行上限。人脸比对约 6-8 步：看图、检测、比对、提交、收尾。
	complianceRunMaxCredits = 50
	complianceRunMaxSteps   = 16
)

type ComplianceImageInput struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
}

// ComplianceAgentModel 是承载核验的 Agent 使用的渠道文本模型，需要支持图片输入。
type ComplianceAgentModel struct {
	ChannelID       string `json:"channelId"`
	ChannelModelKey string `json:"channelModelKey"`
}

type CreateComplianceCheckRequest struct {
	Kind        string                 `json:"kind"`
	Prompt      string                 `json:"prompt"`
	SubjectName string                 `json:"subjectName"`
	Target      ComplianceImageInput   `json:"target"`
	Bases       []ComplianceImageInput `json:"bases"`
	AgentModel  ComplianceAgentModel   `json:"agentModel"`
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
	// Steps 由比对工具实际产出的证据推导，反映 Agent 做到了哪一步。
	Steps []compliance.Step `json:"steps"`
	// RunID 是承载这次核验的 Agent 运行；SkillVersion 是当时使用的合规技能版本。
	RunID        string `json:"runId"`
	SkillVersion string `json:"skillVersion"`
	// Thresholds 是判定一致/疑似的相似度阈值，结果页的规则说明据此生成，前端不写死。
	Thresholds ComplianceThresholds   `json:"thresholds"`
	Target     ComplianceImageInput   `json:"target"`
	Bases      []ComplianceImageInput `json:"bases"`
	Result     *compliance.Report     `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	CreatedAt  time.Time              `json:"createdAt"`
	UpdatedAt  time.Time              `json:"updatedAt"`
}

func (s *Service) faceEngine() compliance.FaceEngine {
	if s.complianceEngine != nil {
		return s.complianceEngine
	}
	return compliance.MockFaceEngine{}
}

func complianceRunKey(checkID string) string { return "compliance-" + checkID }

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
	for _, r := range prompt {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return nil, kernel.BadAuthRequest("核验说明不能包含控制字符")
		}
	}
	if len([]rune(subject)) > complianceNameLimit {
		return nil, kernel.BadAuthRequest("授权人姓名过长")
	}
	if len(req.Bases) == 0 || len(req.Bases) > compliance.MaxBaseImages {
		return nil, kernel.BadAuthRequest("授权底图需要 1 到 3 张")
	}
	agentRequest := CloudAgentRequest{
		Model: strings.TrimSpace(req.AgentModel.ChannelModelKey), ChannelID: strings.TrimSpace(req.AgentModel.ChannelID), ChannelModelKey: strings.TrimSpace(req.AgentModel.ChannelModelKey),
		PermissionMode: "read_only", ReasoningMode: "off",
	}

	now := time.Now().UTC()
	checkID := "chk_" + kernel.NewID()
	check := &model.ComplianceCheck{
		ID: checkID, UserID: actor.ID, Kind: req.Kind, Prompt: prompt, SubjectName: subject,
		Status: compliance.StatusRunning, RunID: cloudAgentID(actor.ID, complianceRunKey(checkID)), CreatedAt: now, UpdatedAt: now,
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
	// 核验靠 Agent 看图，模型不支持图片输入时在落库前就拒绝，不留下一个注定失败的记录。
	if agentRequest.ChannelID == "" || agentRequest.ChannelModelKey == "" || !s.cloudAgentVisionEnabled(agentRequest) {
		return nil, kernel.BadAuthRequest("请选择支持图片输入的文本模型来执行核验")
	}
	if err := s.repo.CreateComplianceCheck(check, images); err != nil {
		return nil, err
	}

	agentRequest.Prompt = complianceRunPrompt(prompt, subject, images)
	agentRequest.ComplianceCheckID = check.ID
	agentRequest.IdempotencyKey = complianceRunKey(check.ID)
	agentRequest.Budget.MaxCredits = complianceRunMaxCredits
	agentRequest.Budget.MaxSteps = complianceRunMaxSteps
	run, err := s.CreateCloudAgentRun(actor.ID, agentRequest, "")
	if err != nil {
		slog.Warn("发起合规核验的 Agent 运行失败", "checkId", check.ID, "error", err)
		if _, finishErr := s.repo.FinishComplianceCheck(check.ID, compliance.StatusFailed, 0, "", "无法启动核验，请稍后重试"); finishErr != nil {
			slog.Warn("合规核验收尾失败", "checkId", check.ID, "error", finishErr)
		}
		return nil, err
	}
	if version := s.complianceSkillVersion(actor.ID, run.ID); version != "" {
		if err := s.repo.SetComplianceCheckRun(check.ID, run.ID, version); err != nil {
			slog.Warn("记录合规技能版本失败", "checkId", check.ID, "error", err)
		}
	}
	return s.ComplianceCheck(actor.ID, check.ID)
}

// complianceRunPrompt 是交给 Agent 的任务描述：用户的说明加素材清单。流程与规则在合规技能里，不在这里。
func complianceRunPrompt(prompt, subject string, images []model.ComplianceCheckImage) string {
	var target string
	bases := 0
	for _, image := range images {
		if image.Role == model.ComplianceImageTarget {
			target = image.Name
		} else {
			bases++
		}
	}
	detail := []string{fmt.Sprintf("待核验素材：%s", firstNonEmpty(target, "未命名")), fmt.Sprintf("授权底图 %d 张", bases)}
	if subject != "" {
		detail = append(detail, "授权人："+subject)
	}
	request := "请按合规技能完成这次人脸比对核验，并用 compliance_submit_report 提交结论。"
	if prompt != "" {
		request = prompt + "\n\n" + request
	}
	return request + "（" + strings.Join(detail, "；") + "）"
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
	if check.Status == compliance.StatusRunning {
		if reason := s.complianceAbandonedReason(userID, check); reason != "" {
			if _, err := s.repo.FinishComplianceCheck(check.ID, compliance.StatusFailed, check.StepsDone, "", reason); err != nil {
				return nil, err
			}
			// 重新读取：收尾是有条件的，可能刚好被提交报告抢先写成了完成。
			if check, images, err = s.repo.ComplianceCheckForUser(userID, id); err != nil {
				return nil, err
			}
		}
	}
	view := &ComplianceCheckView{
		ID: check.ID, Kind: check.Kind, Prompt: check.Prompt, SubjectName: check.SubjectName, Status: check.Status,
		RunID: check.RunID, SkillVersion: check.SkillVersion,
		Thresholds: ComplianceThresholds{Match: compliance.MatchThreshold, Suspect: compliance.SuspectThreshold}, Bases: []ComplianceImageInput{}, Error: check.Error,
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
	evidence, err := s.complianceEvidence(userID, check.ID, len(view.Bases))
	if err != nil {
		return nil, err
	}
	view.Steps = compliance.Steps(evidence, check.Status)
	if check.ResultJSON != "" {
		var result compliance.Report
		if err := json.Unmarshal([]byte(check.ResultJSON), &result); err != nil {
			return nil, err
		}
		view.Result = &result
	}
	return view, nil
}

// complianceAbandonedReason 判断运行中的核验是否已经没人在做了：长时间没有进展，或承载它的 Agent 运行已结束
// 却没有提交结论（比如 Agent 提前停下，或服务重启丢了运行）。返回空串表示仍在正常进行。
func (s *Service) complianceAbandonedReason(userID string, check *model.ComplianceCheck) string {
	if time.Since(check.UpdatedAt) > complianceStaleAfter {
		return "核验超时或服务已重启，请重新发起"
	}
	if check.RunID == "" {
		return ""
	}
	status, err := s.repo.CloudAgentRunStatus(userID, check.RunID)
	if err != nil {
		return ""
	}
	if cloudAgentRunTerminal(status) {
		return "Agent 已结束但没有给出核验结论，请重新发起"
	}
	return ""
}

// complianceEvidence 把证据表还原成 compliance.Evidence。
func (s *Service) complianceEvidence(userID, checkID string, baseCount int) (compliance.Evidence, error) {
	rows, err := s.repo.ComplianceEvidenceForCheck(userID, checkID)
	if err != nil {
		return compliance.Evidence{}, err
	}
	evidence := compliance.Evidence{BaseCount: baseCount, Detect: map[string][]compliance.DetectedFace{}}
	for _, row := range rows {
		switch {
		case row.EvidenceKey == compliance.InspectKey:
			evidence.Inspected = true
		case strings.HasPrefix(row.EvidenceKey, "detect:"):
			var faces []compliance.DetectedFace
			if err := json.Unmarshal([]byte(row.ResultJSON), &faces); err != nil {
				return compliance.Evidence{}, err
			}
			evidence.Detect[strings.TrimPrefix(row.EvidenceKey, "detect:")] = faces
		case strings.HasPrefix(row.EvidenceKey, "compare:"):
			var fact compliance.CompareFact
			if err := json.Unmarshal([]byte(row.ResultJSON), &fact); err != nil {
				return compliance.Evidence{}, err
			}
			evidence.Compares = append(evidence.Compares, fact)
		}
	}
	return evidence, nil
}
