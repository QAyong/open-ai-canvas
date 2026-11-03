package handler

import (
	"net/http"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterComplianceRoutes 注册合规核验接口。归属校验在 service 里做，这里只取登录用户并转发。
func RegisterComplianceRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/compliance/checks", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.CreateComplianceCheckRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		check, err := svc.CreateComplianceCheck(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, check)
	})

	r.GET("/compliance/checks/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		check, err := svc.ComplianceCheck(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, check)
	})
}
