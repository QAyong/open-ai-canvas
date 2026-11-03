package repository

import (
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
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

// UpdateComplianceCheckProgress 只推进仍在运行的核验，进度不会回退。
func (r *Repository) UpdateComplianceCheckProgress(id string, stepsDone int) error {
	return r.db.Model(&model.ComplianceCheck{}).
		Where("id = ? AND status = ? AND steps_done < ?", id, compliance.StatusRunning, stepsDone).
		Updates(map[string]any{"steps_done": stepsDone, "updated_at": time.Now().UTC()}).Error
}

// FinishComplianceCheck 把仍在运行的核验收尾为 done 或 failed；已收尾的不再改写。
func (r *Repository) FinishComplianceCheck(id, status string, stepsDone int, resultJSON, failure string) error {
	return r.db.Model(&model.ComplianceCheck{}).
		Where("id = ? AND status = ?", id, compliance.StatusRunning).
		Updates(map[string]any{"status": status, "steps_done": stepsDone, "result_json": resultJSON, "error": failure, "updated_at": time.Now().UTC()}).Error
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
