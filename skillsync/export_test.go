package skillsync

func ParseSkillProjectionForTest(text []byte) (skillProjection, error) {
	return parseSkillProjection(text)
}

func RenderSkillForTest(name, description, canonicalName string, projection skillProjection, includeSourceKeys bool, banner string) string {
	return renderSkill(name, description, canonicalName, projection, includeSourceKeys, banner)
}
