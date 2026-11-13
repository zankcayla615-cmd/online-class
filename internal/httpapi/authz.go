package httpapi

import "onlineclass/internal/store"

// Action 是可授权的操作类型。
type Action string

// 授权动作常量。
const (
	ActionViewAssignment Action = "view_assignment"
	ActionSubmit         Action = "submit"
	ActionReview         Action = "review"
	ActionGrantExtension Action = "grant_extension"
	ActionRecordReminder Action = "record_reminder"
	ActionViewStudent    Action = "view_student"
)

// Allow 判断某角色能否对目标资源执行动作。
//
// 前置：sameClass 表示目标资源与请求者同班；isSelf 表示目标学生就是请求者本人。
// 跨班一律拒绝（由调用方转成 404，避免泄露资源是否存在）。
func Allow(role string, action Action, sameClass, isSelf bool) bool {
	if !sameClass {
		return false
	}
	switch action {
	case ActionViewAssignment:
		// 同班的教师、助教、学生均可查看作业。
		return role == store.RoleTeacher || role == store.RoleAssistant || role == store.RoleStudent
	case ActionSubmit:
		return role == store.RoleStudent && isSelf
	case ActionReview, ActionGrantExtension:
		return role == store.RoleTeacher
	case ActionRecordReminder:
		return role == store.RoleAssistant
	case ActionViewStudent:
		if role == store.RoleTeacher || role == store.RoleAssistant {
			return true
		}
		return role == store.RoleStudent && isSelf
	}
	return false
}
