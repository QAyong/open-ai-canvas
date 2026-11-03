package app

import (
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/compliance"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestComplianceService(t *testing.T) (*Service, *gorm.DB, *model.User, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Resource{}, &model.ComplianceCheck{}, &model.ComplianceCheckImage{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{repo: repository.New(db), complianceComparer: compliance.MockFaceComparer{}}
	alice := &model.User{ID: "user_alice", Username: "alice", Role: "user", Status: "active", CreatedAt: time.Now()}
	bob := &model.User{ID: "user_bob", Username: "bob", Role: "user", Status: "active", CreatedAt: time.Now()}
	for _, user := range []*model.User{alice, bob} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	return svc, db, alice, bob
}

func seedComplianceResource(t *testing.T, db *gorm.DB, owner, id, kind string, status model.ResourceStatus) {
	t.Helper()
	if err := db.Create(&model.Resource{ID: id, UserID: owner, Kind: kind, Status: status}).Error; err != nil {
		t.Fatal(err)
	}
}

func faceCheckRequest(target string, bases ...string) CreateComplianceCheckRequest {
	req := CreateComplianceCheckRequest{Kind: compliance.KindFaceCompare, SubjectName: "张三", Target: ComplianceImageInput{ResourceID: target, Name: "still.png"}}
	for _, base := range bases {
		req.Bases = append(req.Bases, ComplianceImageInput{ResourceID: base, Name: base + ".png"})
	}
	return req
}

func waitForComplianceStatus(t *testing.T, svc *Service, userID, id, want string) *ComplianceCheckView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, err := svc.ComplianceCheck(userID, id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == want {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("核验状态 %s，等待 %s 超时", view.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCreateComplianceCheckRunsToCompletion(t *testing.T) {
	svc, db, alice, _ := newTestComplianceService(t)
	seedComplianceResource(t, db, alice.ID, "res_target", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_base1", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_base2", "image", model.ResourceStatusReady)

	created, err := svc.CreateComplianceCheck(alice, faceCheckRequest("res_target", "res_base1", "res_base2"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Target.ResourceID != "res_target" || len(created.Bases) != 2 || created.StepCount != compliance.StepCount {
		t.Fatalf("创建结果不完整：%+v", created)
	}
	done := waitForComplianceStatus(t, svc, alice.ID, created.ID, compliance.StatusDone)
	if done.StepsDone != compliance.StepCount || done.Result == nil || len(done.Result.Faces) == 0 || done.Result.Overall == "" {
		t.Fatalf("完成的核验应带有结果：%+v", done)
	}
}

func TestCreateComplianceCheckRejectsForeignAndUnreadyResources(t *testing.T) {
	svc, db, alice, bob := newTestComplianceService(t)
	seedComplianceResource(t, db, alice.ID, "res_target", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, bob.ID, "res_bob", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_video", "video", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_pending", "image", model.ResourceStatusPending)

	for name, req := range map[string]CreateComplianceCheckRequest{
		"别人的资源":  faceCheckRequest("res_target", "res_bob"),
		"不是图片":   faceCheckRequest("res_target", "res_video"),
		"还没上传完成": faceCheckRequest("res_target", "res_pending"),
		"资源不存在":  faceCheckRequest("res_target", "res_missing"),
		"没有底图":   faceCheckRequest("res_target"),
		"底图超过上限": faceCheckRequest("res_target", "a", "b", "c", "d"),
		"目标缺少资源": faceCheckRequest("", "res_target"),
		"底图重复":   faceCheckRequest("res_target", "res_target", "res_target"),
	} {
		var appErr *kernel.AppError
		if _, err := svc.CreateComplianceCheck(alice, req); err == nil || !errors.As(err, &appErr) || appErr.Status != 400 {
			t.Errorf("%s：应返回 400，得到 %v", name, err)
		}
	}
	var count int64
	db.Model(&model.ComplianceCheck{}).Count(&count)
	if count != 0 {
		t.Fatalf("被拒绝的请求不应留下核验记录，现有 %d 条", count)
	}
}

func TestComplianceCheckIsOnlyVisibleToOwner(t *testing.T) {
	svc, db, alice, bob := newTestComplianceService(t)
	seedComplianceResource(t, db, alice.ID, "res_target", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_base", "image", model.ResourceStatusReady)
	created, err := svc.CreateComplianceCheck(alice, faceCheckRequest("res_target", "res_base"))
	if err != nil {
		t.Fatal(err)
	}
	var appErr *kernel.AppError
	if _, err := svc.ComplianceCheck(bob.ID, created.ID); !errors.As(err, &appErr) || appErr.Status != 404 {
		t.Fatalf("别人读取应返回 404，得到 %v", err)
	}
	if _, err := svc.CreateComplianceCheck(nil, faceCheckRequest("res_target", "res_base")); !errors.As(err, &appErr) || appErr.Status != 401 {
		t.Fatalf("未登录应返回 401，得到 %v", err)
	}
}

func TestStaleRunningComplianceCheckBecomesFailed(t *testing.T) {
	svc, db, alice, _ := newTestComplianceService(t)
	old := time.Now().UTC().Add(-2 * complianceStaleAfter)
	check := &model.ComplianceCheck{ID: "chk_stale", UserID: alice.ID, Kind: compliance.KindFaceCompare, Status: compliance.StatusRunning, StepsDone: 1, CreatedAt: old, UpdatedAt: old}
	if err := db.Create(check).Error; err != nil {
		t.Fatal(err)
	}
	view, err := svc.ComplianceCheck(alice.ID, check.ID)
	if err != nil || view.Status != compliance.StatusFailed || view.Error == "" || view.Result != nil {
		t.Fatalf("超时的核验应收尾为失败：%+v (%v)", view, err)
	}
}

func TestComplianceImagesBlockResourceDeletion(t *testing.T) {
	svc, db, alice, bob := newTestComplianceService(t)
	seedComplianceResource(t, db, alice.ID, "res_target", "image", model.ResourceStatusReady)
	seedComplianceResource(t, db, alice.ID, "res_base", "image", model.ResourceStatusReady)
	if _, err := svc.CreateComplianceCheck(alice, faceCheckRequest("res_target", "res_base")); err != nil {
		t.Fatal(err)
	}
	refs, err := svc.repo.ComplianceResourceReferences(alice.ID, []string{"res_target", "res_unused"})
	if err != nil || len(refs) != 1 || refs[0].ResourceID != "res_target" || refs[0].Kind != "合规核验" {
		t.Fatalf("核验用到的图片应被登记为引用：%+v (%v)", refs, err)
	}
	if refs, _ := svc.repo.ComplianceResourceReferences(bob.ID, []string{"res_target"}); len(refs) != 0 {
		t.Fatalf("别人的资源引用不应被查到：%+v", refs)
	}
}
