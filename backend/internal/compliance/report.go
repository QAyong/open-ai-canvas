package compliance

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 图片键：Agent 工具用它指代一次核验里的图片，服务端据此取资源，不让模型传资源 ID。
const ImageTarget = "target"

// BaseKey 是第 position 张授权底图（从 0 开始）的图片键，如 base-1。
func BaseKey(position int) string { return fmt.Sprintf("base-%d", position+1) }

// BasePosition 把 base-N 还原成底图位置；不是底图键时 ok 为 false。
func BasePosition(key string) (position int, ok bool) {
	var n int
	if _, err := fmt.Sscanf(key, "base-%d", &n); err != nil || n < 1 || n > MaxBaseImages || BaseKey(n-1) != key {
		return 0, false
	}
	return n - 1, true
}

// 证据键：同一个键只记一次，工具重复调用返回第一次的结果，不会让引擎重复执行。
const InspectKey = "inspect"

func DetectKey(image string) string { return "detect:" + image }

func CompareKey(targetFace int, base string, baseFace int) string {
	return fmt.Sprintf("compare:%d:%s:%d", targetFace, base, baseFace)
}

type ImageStyle string

const (
	StyleRealistic ImageStyle = "realistic"
	StyleStylized  ImageStyle = "stylized"
	StyleUnclear   ImageStyle = "unclear"
)

// CompareFact 是一次引擎比对的事实：待核验图第 TargetFace 张脸与底图 Base 的第 BaseFace 张脸。
type CompareFact struct {
	TargetFace int    `json:"targetFace"`
	Base       string `json:"base"`
	BaseFace   int    `json:"baseFace"`
	Similarity int    `json:"similarity"`
}

// Evidence 是本次核验里引擎工具已经产出的事实，由服务端从证据表还原。
type Evidence struct {
	BaseCount int
	Inspected bool
	// Detect 的键是图片键（target、base-1…）。
	Detect   map[string][]DetectedFace
	Compares []CompareFact
}

func (e Evidence) imageKeys() []string {
	keys := []string{ImageTarget}
	for i := 0; i < e.BaseCount; i++ {
		keys = append(keys, BaseKey(i))
	}
	return keys
}

// Step 是结果页步骤条里的一步，由证据推导，反映 Agent 实际做到哪里。
type Step struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State: done | active | pending。
	State string `json:"state"`
}

const StepCount = 4

// Steps 按证据推导步骤。核验完成时全部完成；运行中第一个未完成的步骤为进行中；失败后未完成的步骤保持待处理。
func Steps(e Evidence, status string) []Step {
	detected := true
	for _, key := range e.imageKeys() {
		if _, ok := e.Detect[key]; !ok {
			detected = false
		}
	}
	done := []bool{e.Inspected, detected, len(e.Compares) > 0, false}
	labels := []struct{ id, label string }{{"inspect", "查看图片"}, {"detect", "检测人脸"}, {"compare", "比对图片"}, {"report", "生成结论"}}
	steps := make([]Step, len(labels))
	active := false
	for i, item := range labels {
		state := "pending"
		switch {
		case status == StatusDone || done[i]:
			state = "done"
		case status == StatusRunning && !active:
			state, active = "active", true
		}
		steps[i] = Step{ID: item.id, Label: item.label, State: state}
	}
	return steps
}

// StepsDone 是已完成的步骤数，写进核验记录供进度只增不减。
func StepsDone(e Evidence, status string) int {
	count := 0
	for _, step := range Steps(e, status) {
		if step.State == "done" {
			count++
		}
	}
	return count
}

const Disclaimer = "本结果为面部特征相似度的技术分析，仅作合规审查的辅助材料，不能替代司法鉴定、律师意见或法院判断。"

// FaceNote 是 Agent 对待核验图里某一张脸写下的判断；数字和人脸框不由它提供。
type FaceNote struct {
	Index       int      `json:"index"`
	Basis       []string `json:"basis"`
	Limitations []string `json:"limitations,omitempty"`
}

// SubmitInput 是 Agent 提交报告时能写的全部内容：判断与文字，不含相似度、人脸框或每张脸的结论。
type SubmitInput struct {
	Summary     string     `json:"summary"`
	Style       ImageStyle `json:"style"`
	Faces       []FaceNote `json:"faces"`
	Suggestions []string   `json:"suggestions,omitempty"`
	// EscalateTo 只能把整体结论往上提（review 或 risk），并且必须写理由；不能压低引擎给出的结论。
	EscalateTo        OverallVerdict `json:"escalateTo,omitempty"`
	EscalateReason    string         `json:"escalateReason,omitempty"`
	HumanReviewReason string         `json:"humanReviewReason,omitempty"`
}

type ReportFace struct {
	FaceItem
	Basis       []string `json:"basis"`
	Limitations []string `json:"limitations"`
}

type Escalation struct {
	To     OverallVerdict `json:"to"`
	Reason string         `json:"reason"`
}

type HumanReview struct {
	Required bool   `json:"required"`
	Reason   string `json:"reason,omitempty"`
}

// Report 是落库并展示在结果页的最终结果。Overall 与每张脸的 Verdict 都由服务端按阈值算出。
type Report struct {
	Overall     OverallVerdict `json:"overall"`
	Style       ImageStyle     `json:"style"`
	Summary     string         `json:"summary"`
	Faces       []ReportFace   `json:"faces"`
	Suggestions []string       `json:"suggestions"`
	Escalation  *Escalation    `json:"escalation,omitempty"`
	HumanReview HumanReview    `json:"humanReview"`
	Disclaimer  string         `json:"disclaimer"`
}

const (
	summaryRunes = 600
	itemRunes    = 200
	maxItems     = 6
)

// 这些表述把相似度说成法律结论或身份确认，合规口径不允许。
var bannedPhrases = []string{"确认侵权", "确认同一人", "确定是同一人", "确认不侵权", "不构成侵权", "未侵权", "不侵权", "绝对安全", "放心商用", "可以商用", "一定侵权"}

// checkWording 拒绝结论式措辞。前面紧跟否定字（如"不能确认侵权"）的说法是在划边界，放行。
func checkWording(label, text string) error {
	for _, phrase := range bannedPhrases {
		for offset := 0; ; {
			at := strings.Index(text[offset:], phrase)
			if at < 0 {
				break
			}
			at += offset
			if !negatedBefore(text[:at]) {
				return fmt.Errorf("%s含有不允许的结论式措辞「%s」；请改用「存在疑似风险」「建议人工复核」「当前比对范围内」等审慎表述", label, phrase)
			}
			offset = at + len(phrase)
		}
	}
	return nil
}

// negatedBefore 看措辞前面同一分句里的几个字有没有否定字，如「不能据此确认侵权」。
func negatedBefore(prefix string) bool {
	runes := []rune(prefix)
	for i := len(runes) - 1; i >= 0 && i >= len(runes)-6; i-- {
		if strings.ContainsRune("，。；：、！？,.;:!?\n", runes[i]) {
			return false
		}
		if strings.ContainsRune("不未无非勿别", runes[i]) {
			return true
		}
	}
	return false
}

func checkText(label, text string, limit int, required bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		if required {
			return "", fmt.Errorf("%s不能为空", label)
		}
		return "", nil
	}
	if utf8.RuneCountInString(text) > limit {
		return "", fmt.Errorf("%s不能超过 %d 个字", label, limit)
	}
	return text, checkWording(label, text)
}

func checkList(label string, items []string, requireOne bool) ([]string, error) {
	if len(items) > maxItems {
		return nil, fmt.Errorf("%s最多 %d 条", label, maxItems)
	}
	if requireOne && len(items) == 0 {
		return nil, fmt.Errorf("%s至少写一条", label)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, err := checkText(label, item, itemRunes, true)
		if err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, nil
}

// BuildReport 把 Agent 的陈述和引擎证据对齐，生成最终结果。返回的错误会原样回给 Agent，让它补做缺失的工具调用或改写。
//
// 规则：
//   - 每张检出的脸都必须已和至少一张授权底图比对，并且 Agent 恰好为它写了一次判断；
//   - 相似度取该脸与各底图的最高值，人脸框取检测证据，每张脸的结论按阈值算，整体结论按 OverallOf 算；
//   - 画风不是 realistic 时本地相似度只供参考，整体结论不能是通过；
//   - Agent 只能把整体结论升到 review 或 risk，不能压低。
func BuildReport(e Evidence, in SubmitInput) (Report, error) {
	targetFaces, detected := e.Detect[ImageTarget]
	if !detected {
		return Report{}, errors.New("还没有检测待核验图的人脸；请先调用 compliance_detect_faces")
	}
	switch in.Style {
	case StyleRealistic, StyleStylized, StyleUnclear:
	default:
		return Report{}, errors.New("style 必须是 realistic、stylized 或 unclear")
	}
	summary, err := checkText("summary", in.Summary, summaryRunes, true)
	if err != nil {
		return Report{}, err
	}
	suggestions, err := checkList("suggestions", in.Suggestions, false)
	if err != nil {
		return Report{}, err
	}

	notes := make(map[int]FaceNote, len(in.Faces))
	for _, note := range in.Faces {
		if _, dup := notes[note.Index]; dup {
			return Report{}, fmt.Errorf("第 %d 张脸写了不止一次判断", note.Index)
		}
		notes[note.Index] = note
	}
	known := make(map[int]DetectedFace, len(targetFaces))
	for _, face := range targetFaces {
		known[face.Index] = face
	}
	for index := range notes {
		if _, ok := known[index]; !ok {
			return Report{}, fmt.Errorf("待核验图里没有检出第 %d 张脸", index)
		}
	}

	faces := make([]ReportFace, 0, len(targetFaces))
	items := make([]FaceItem, 0, len(targetFaces))
	for _, detectedFace := range targetFaces {
		best, bestBase, compared := 0, 0, false
		for _, fact := range e.Compares {
			if fact.TargetFace != detectedFace.Index {
				continue
			}
			position, ok := BasePosition(fact.Base)
			if !ok {
				continue
			}
			if !compared || fact.Similarity > best {
				best, bestBase, compared = fact.Similarity, position, true
			}
		}
		if !compared {
			return Report{}, fmt.Errorf("第 %d 张脸还没有和授权底图比对；请先调用 compliance_compare_faces", detectedFace.Index)
		}
		note, ok := notes[detectedFace.Index]
		if !ok {
			return Report{}, fmt.Errorf("缺少第 %d 张脸的判断；faces 要为每张检出的脸写一条", detectedFace.Index)
		}
		basis, err := checkList(fmt.Sprintf("第 %d 张脸的 basis", detectedFace.Index), note.Basis, true)
		if err != nil {
			return Report{}, err
		}
		limitations, err := checkList(fmt.Sprintf("第 %d 张脸的 limitations", detectedFace.Index), note.Limitations, false)
		if err != nil {
			return Report{}, err
		}
		item := FaceItem{Index: detectedFace.Index, Box: detectedFace.Box, Similarity: best, BestBaseIndex: bestBase, Verdict: VerdictOf(best)}
		items = append(items, item)
		faces = append(faces, ReportFace{FaceItem: item, Basis: basis, Limitations: limitations})
	}

	overall := OverallOf(items)
	reasons := []string{}
	if reason, err := checkText("humanReviewReason", in.HumanReviewReason, itemRunes, false); err != nil {
		return Report{}, err
	} else if reason != "" {
		reasons = append(reasons, reason)
	}
	if overall == OverallPass && in.Style != StyleRealistic {
		overall = OverallReview
		reasons = append(reasons, "画风为风格化或无法确认，本地相似度只供参考，需要人工复核")
	}

	var escalation *Escalation
	if in.EscalateTo != "" {
		if in.EscalateTo != OverallReview && in.EscalateTo != OverallRisk {
			return Report{}, errors.New("escalateTo 只能是 review 或 risk")
		}
		reason, err := checkText("escalateReason", in.EscalateReason, itemRunes, true)
		if err != nil {
			return Report{}, err
		}
		if overall != OverallNoFace && severity(in.EscalateTo) > severity(overall) {
			overall = in.EscalateTo
			escalation = &Escalation{To: in.EscalateTo, Reason: reason}
			reasons = append(reasons, reason)
		}
	}

	switch overall {
	case OverallReview, OverallRisk:
		if len(reasons) == 0 {
			reasons = append(reasons, "存在疑似或未与授权人匹配的人脸，需要人工复核")
		}
	case OverallNoFace:
		reasons = append(reasons, "没有检出人脸，需要人工确认素材是否可用")
	}

	if suggestions == nil {
		suggestions = []string{}
	}
	return Report{
		Overall: overall, Style: in.Style, Summary: summary, Faces: faces, Suggestions: suggestions, Escalation: escalation,
		HumanReview: HumanReview{Required: len(reasons) > 0, Reason: strings.Join(reasons, "；")},
		Disclaimer:  Disclaimer,
	}, nil
}
