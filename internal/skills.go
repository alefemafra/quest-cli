package internal

import "embed"

//go:embed all:skills
var skillsFS embed.FS

func ReadSkill(name string) string {
	data, err := skillsFS.ReadFile("skills/" + name + ".md")
	if err != nil {
		return ""
	}
	return string(data)
}

func ReadTemplate(name string) string {
	data, err := skillsFS.ReadFile("skills/templates/" + name + ".md")
	if err != nil {
		return ""
	}
	return string(data)
}
