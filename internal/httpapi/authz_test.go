package httpapi_test

import (
	"testing"

	"onlineclass/internal/httpapi"
	"onlineclass/internal/store"
)

func TestAllowMatrix(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		action    httpapi.Action
		sameClass bool
		isSelf    bool
		want      bool
	}{
		{"教师看本班作业", store.RoleTeacher, httpapi.ActionViewAssignment, true, false, true},
		{"助教看本班作业", store.RoleAssistant, httpapi.ActionViewAssignment, true, false, true},
		{"学生看本班作业", store.RoleStudent, httpapi.ActionViewAssignment, true, false, true},
		{"学生看外班作业", store.RoleStudent, httpapi.ActionViewAssignment, false, false, false},

		{"学生提交自己", store.RoleStudent, httpapi.ActionSubmit, true, true, true},
		{"学生提交他人", store.RoleStudent, httpapi.ActionSubmit, true, false, false},
		{"教师提交", store.RoleTeacher, httpapi.ActionSubmit, true, true, false},
		{"助教提交", store.RoleAssistant, httpapi.ActionSubmit, true, false, false},

		{"教师批阅", store.RoleTeacher, httpapi.ActionReview, true, false, true},
		{"助教批阅", store.RoleAssistant, httpapi.ActionReview, true, false, false},
		{"学生批阅", store.RoleStudent, httpapi.ActionReview, true, true, false},
		{"教师批外班", store.RoleTeacher, httpapi.ActionReview, false, false, false},

		{"教师批准补交", store.RoleTeacher, httpapi.ActionGrantExtension, true, false, true},
		{"助教批准补交", store.RoleAssistant, httpapi.ActionGrantExtension, true, false, false},
		{"学生自己申请补交", store.RoleStudent, httpapi.ActionGrantExtension, true, true, false},

		{"助教记录催交", store.RoleAssistant, httpapi.ActionRecordReminder, true, false, true},
		{"教师记录催交", store.RoleTeacher, httpapi.ActionRecordReminder, true, false, false},
		{"学生记录催交", store.RoleStudent, httpapi.ActionRecordReminder, true, true, false},

		{"学生看自己", store.RoleStudent, httpapi.ActionViewStudent, true, true, true},
		{"学生看同学", store.RoleStudent, httpapi.ActionViewStudent, true, false, false},
		{"教师看学生", store.RoleTeacher, httpapi.ActionViewStudent, true, false, true},
		{"助教看学生", store.RoleAssistant, httpapi.ActionViewStudent, true, false, true},
		{"学生看外班学生", store.RoleStudent, httpapi.ActionViewStudent, false, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := httpapi.Allow(c.role, c.action, c.sameClass, c.isSelf); got != c.want {
				t.Fatalf("Allow(%s, %s, sameClass=%v, isSelf=%v) = %v, 期望 %v",
					c.role, c.action, c.sameClass, c.isSelf, got, c.want)
			}
		})
	}
}

func TestAllowUnknownAction(t *testing.T) {
	if httpapi.Allow(store.RoleTeacher, httpapi.Action("unknown"), true, false) {
		t.Fatal("未知动作应默认拒绝")
	}
}
