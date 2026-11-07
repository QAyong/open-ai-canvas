package model

import "time"

// ComplianceCheck 是一次合规核验（目前只有人脸比对）。核验由 Agent 按技能调用比对工具完成，
// 工具产出的事实记在 ComplianceCheckEvidence，Agent 提交的报告经 compliance.BuildReport 对齐后写入 ResultJSON；
// 判定规则见 internal/compliance，页面只读取这份记录。
type ComplianceCheck struct {
	ID          string `gorm:"primaryKey;size:64" json:"id"`
	UserID      string `gorm:"index;size:36" json:"userId"`
	Kind        string `gorm:"size:32" json:"kind"`
	Prompt      string `gorm:"type:text" json:"prompt"`
	SubjectName string `gorm:"size:128" json:"subjectName"`
	// Status: running | done | failed。
	Status string `gorm:"index;size:16" json:"status"`
	// StepsDone 是已完成的步骤数，由证据推导，只增不减。
	StepsDone int `json:"stepsDone"`
	// RunID 是承载这次核验的 Agent 运行；运行结束而核验仍在运行时，读取时收尾为失败。
	RunID string `gorm:"size:80" json:"runId"`
	// SkillVersion 是本次核验使用的合规技能版本，让结果可以追溯到当时的规则。
	SkillVersion string `gorm:"size:80" json:"skillVersion"`
	// ResultJSON 是 compliance.Report 的 JSON，核验完成前为空。
	ResultJSON string    `gorm:"type:text" json:"-"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `gorm:"index" json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ComplianceCheckEvidence 是核验里比对工具产出的事实（检测结果、相似度）。同一核验里同一个 EvidenceKey 只记一次，
// 工具重复调用返回第一次的结果；提交报告时服务端只信这里的数字，不信 Agent 的陈述。
type ComplianceCheckEvidence struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	CheckID     string    `gorm:"uniqueIndex:idx_compliance_evidence_key,priority:1;size:64" json:"checkId"`
	EvidenceKey string    `gorm:"uniqueIndex:idx_compliance_evidence_key,priority:2;size:96" json:"evidenceKey"`
	UserID      string    `gorm:"index;size:36" json:"userId"`
	Seq         int       `json:"seq"`
	Tool        string    `gorm:"size:64" json:"tool"`
	ResultJSON  string    `gorm:"type:text" json:"-"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ComplianceCheckImage 记录核验用到的图片。单独成表是为了让资源删除校验能按 resource_id 查到引用。
type ComplianceCheckImage struct {
	ID         string    `gorm:"primaryKey;size:64" json:"id"`
	CheckID    string    `gorm:"index;size:64" json:"checkId"`
	UserID     string    `gorm:"index;size:36" json:"userId"`
	Role       string    `gorm:"size:16" json:"role"` // target | base
	Position   int       `json:"position"`
	ResourceID string    `gorm:"index;size:36" json:"resourceId"`
	Name       string    `gorm:"size:256" json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
}

const (
	ComplianceImageTarget = "target"
	ComplianceImageBase   = "base"
)
