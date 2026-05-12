package controller

import (
	"embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed skill_docs/llms.txt
var sub2apiAgentLLMS []byte

//go:embed skill_docs/skills/*.md
var sub2apiAgentSkillsFS embed.FS

// GetSub2APIAgentLLMS serves the umbrella agent-skill specification at
// /api/sub2api/llms.txt. The content is the embedded skill_docs/llms.txt
// markdown file kept in this package.
func GetSub2APIAgentLLMS(c *gin.Context) {
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", sub2apiAgentLLMS)
}

// GetSub2APIAgentSkill serves a single skill specification at
// /api/sub2api/skills/{name}. The {name} path parameter is mapped directly
// to skill_docs/skills/{name}.md. Names that contain '/', '\\', or '.' are
// rejected up front to prevent path traversal before hitting the embed FS.
func GetSub2APIAgentSkill(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" || strings.ContainsAny(name, "/\\.") {
		c.Status(http.StatusNotFound)
		return
	}
	body, err := sub2apiAgentSkillsFS.ReadFile(fmt.Sprintf("skill_docs/skills/%s.md", name))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", body)
}
