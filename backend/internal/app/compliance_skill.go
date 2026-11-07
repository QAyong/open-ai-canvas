package app

// 合规核验靠内置技能「肖像比对合规」驱动：Agent 按技能里的流程调用比对工具。
// 技能不要求用户已安装——合规 run 由服务端按绑定的核验注入，用户卸载或停用技能库里的副本不影响核验；
// 注入的版本在建 run 时冻结，并记到核验上，结果可以追溯到当时的规则。
const complianceSkillID = "yingce-portrait-face-compare"

// complianceSystemSkills 返回本轮需要服务端注入的系统技能。
func complianceSystemSkills(req CloudAgentRequest) []string {
	if req.ComplianceCheckID == "" {
		return nil
	}
	return []string{complianceSkillID}
}

// complianceSkillIsSystem 判断某技能在本轮是不是服务端注入的合规技能：只有绑定了核验的运行才可读。
func complianceSkillIsSystem(req CloudAgentRequest, skillID string) bool {
	return req.ComplianceLinked && skillID == complianceSkillID
}

// complianceSkillVersion 读取运行里冻结的合规技能版本；读不到返回空串。
func (s *Service) complianceSkillVersion(userID, runID string) string {
	_, state, err := s.cloudAgentTask(userID, runID)
	if err != nil {
		return ""
	}
	return state.SkillVersions[complianceSkillID]
}
