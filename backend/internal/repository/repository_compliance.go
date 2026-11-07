package repository

import (
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateComplianceCheck 在一个事务里写入核验和它用到的图片。
func (r *Repository) CreateComplianceCheck(check *model.ComplianceCheck, images []model.ComplianceCheckImage) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(check).Error; err != nil {
			return err
		}
		return tx.Create(&images).Error
	})
}

// ComplianceCheckForUser 只返回属于该用户的核验；找不到返回 gorm.ErrRecordNotFound。
func (r *Repository) ComplianceCheckForUser(userID, id string) (*model.ComplianceCheck, []model.ComplianceCheckImage, error) {
	var check model.ComplianceCheck
	if err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&check).Error; err != nil {
		return nil, nil, err
	}
	var images []model.ComplianceCheckImage
	if err := r.db.Where("check_id = ?", check.ID).Order("role ASC, position ASC").Find(&images).Error; err != nil {
		return nil, nil, err
	}
	return &check, images, nil
}

func (r *Repository) ListComplianceChecksForUser(userID string, limit int) ([]model.ComplianceCheck, error) {
	var list []model.ComplianceCheck
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Limit(limit).Find(&list).Error
	return list, err
}

// UpdateComplianceCheckProgress 只推进仍在运行的核验，进度不会回退；步数不变时也刷新更新时间，表示核验仍有进展。
func (r *Repository) UpdateComplianceCheckProgress(id string, stepsDone int) error {
	return r.db.Model(&model.ComplianceCheck{}).
		Where("id = ? AND status = ? AND steps_done <= ?", id, compliance.StatusRunning, stepsDone).
		Updates(map[string]any{"steps_done": stepsDone, "updated_at": time.Now().UTC()}).Error
}

// FinishComplianceCheck 把仍在运行的核验收尾为 done 或 failed；已收尾的不再改写。
// 返回 false 表示核验已经被别的调用收尾，本次没有写入。
func (r *Repository) FinishComplianceCheck(id, status string, stepsDone int, resultJSON, failure string) (bool, error) {
	result := r.db.Model(&model.ComplianceCheck{}).
		Where("id = ? AND status = ?", id, compliance.StatusRunning).
		Updates(map[string]any{"status": status, "steps_done": stepsDone, "result_json": resultJSON, "error": failure, "updated_at": time.Now().UTC()})
	return result.RowsAffected == 1, result.Error
}

// SetComplianceCheckRun 记录承载这次核验的 Agent 运行和使用的技能版本。
func (r *Repository) SetComplianceCheckRun(id, runID, skillVersion string) error {
	return r.db.Model(&model.ComplianceCheck{}).Where("id = ?", id).
		Updates(map[string]any{"run_id": runID, "skill_version": skillVersion}).Error
}

// RecordComplianceEvidence 记录一条证据。同一核验里同一个 evidenceKey 只记第一次：已存在时不写入，
// 返回已有的那一条（created 为 false），这样工具重复调用拿到的是同一个事实。
func (r *Repository) RecordComplianceEvidence(checkID, userID, evidenceKey, tool, resultJSON string, newID func() string) (evidence *model.ComplianceCheckEvidence, created bool, err error) {
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.ComplianceCheckEvidence{}).Where("check_id = ?", checkID).Count(&count).Error; err != nil {
			return err
		}
		row := &model.ComplianceCheckEvidence{ID: "cev_" + newID(), CheckID: checkID, EvidenceKey: evidenceKey, UserID: userID, Seq: int(count) + 1, Tool: tool, ResultJSON: resultJSON, CreatedAt: time.Now().UTC()}
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if insert.Error != nil {
			return insert.Error
		}
		created = insert.RowsAffected == 1
		var stored model.ComplianceCheckEvidence
		if err := tx.Where("check_id = ? AND evidence_key = ?", checkID, evidenceKey).First(&stored).Error; err != nil {
			return err
		}
		evidence = &stored
		return nil
	})
	return evidence, created, err
}

// ComplianceEvidenceForCheck 按记录顺序返回核验的全部证据。
func (r *Repository) ComplianceEvidenceForCheck(userID, checkID string) ([]model.ComplianceCheckEvidence, error) {
	var rows []model.ComplianceCheckEvidence
	err := r.db.Where("user_id = ? AND check_id = ?", userID, checkID).Order("seq ASC").Find(&rows).Error
	return rows, err
}

// ComplianceResourceReferences 列出引用了这些资源的核验图片，资源删除前据此拒绝删除。
func (r *Repository) ComplianceResourceReferences(userID string, resourceIDs []string) ([]ResourceDirectReference, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	var images []model.ComplianceCheckImage
	if err := r.db.Where("user_id = ? AND resource_id IN ?", userID, resourceIDs).Find(&images).Error; err != nil {
		return nil, err
	}
	references := make([]ResourceDirectReference, 0, len(images))
	for _, image := range images {
		references = append(references, ResourceDirectReference{Kind: "合规核验", ID: image.CheckID, Title: image.Name, ResourceID: image.ResourceID})
	}
	return references, nil
}

// CloudAgentRunStatus 只读 Agent 运行的状态，供合规核验判断承载它的运行是否已经结束。
func (r *Repository) CloudAgentRunStatus(userID, runID string) (status string, err error) {
	var run model.CloudAgentExecution
	if err := r.db.Select("id", "status").Where("id = ? AND user_id = ?", runID, userID).First(&run).Error; err != nil {
		return "", err
	}
	return run.Status, nil
}
