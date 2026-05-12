package controller_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/controller"

	"github.com/gin-gonic/gin"
)

func TestSub2APIAgentSkillEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/sub2api/llms.txt", controller.GetSub2APIAgentLLMS)
	r.GET("/api/sub2api/skills/:name", controller.GetSub2APIAgentSkill)

	cases := []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{"/api/sub2api/llms.txt", http.StatusOK, "Sub2API Agent Skill Specification"},
		{"/api/sub2api/skills/sub2api-marketplace", http.StatusOK, "sub2api-marketplace"},
		{"/api/sub2api/skills/sub2api-sources", http.StatusOK, "Register, list, inspect"},
		{"/api/sub2api/skills/sub2api-grants", http.StatusOK, "Share a Sub2API source"},
		{"/api/sub2api/skills/sub2api-quota-usage", http.StatusOK, "remaining quota and historical usage"},
		{"/api/sub2api/skills/sub2api-inference", http.StatusOK, "OpenAI-compatible inference call"},
		{"/api/sub2api/skills/sub2api-withdrawal", http.StatusOK, "payout request"},
		{"/api/sub2api/skills/sub2api-attest", http.StatusOK, "TEE evidence record"},
		{"/api/sub2api/skills/unknown-skill", http.StatusNotFound, ""},
		{"/api/sub2api/skills/" + strings.Repeat(".", 3), http.StatusNotFound, ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != c.wantCode {
			t.Errorf("%s: code = %d, want %d", c.path, w.Code, c.wantCode)
			continue
		}
		if c.wantBody != "" && !strings.Contains(w.Body.String(), c.wantBody) {
			t.Errorf("%s: body missing %q", c.path, c.wantBody)
		}
	}
}
