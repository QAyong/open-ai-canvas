package model

import "time"

// ComplianceCheck 是一次合规核验（目前只有人脸比对）。结果由比对引擎写入 ResultJSON，
// 判定规则见 internal/compliance；Agent 与页面都只读取这份记录。
type ComplianceCheck struct {
	ID          string `gorm:"primaryKey;size:64" json:"id"`
	UserID      string `gorm:"index;size:36" json:"userId"`
	Kind        string `gorm:"size:32" json:"kind"`
	Prompt      string `gorm:"type:text" json:"prompt"`
	SubjectName string `gorm:"size:128" json:"subjectName"`
	// Status: running | done | failed。
	Status    string `gorm:"index;size:16" json:"status"`
	StepsDone int    `json:"stepsDone"`
	// ResultJSON 是 compliance.FaceCompareResult 的 JSON，核验完成前为空。
	ResultJSON string    `gorm:"type:text" json:"-"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `gorm:"index" json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
