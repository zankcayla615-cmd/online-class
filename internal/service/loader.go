package service

import (
	"context"
	"errors"

	"onlineclass/internal/apperr"
	"onlineclass/internal/store"
)

// loadAssignmentFor 读取作业并做班级隔离：跨班与不存在统一按 404 处理。
//
// 多个服务共用同一个语义，避免"某个入口忘了判班级"。
func loadAssignmentFor(ctx context.Context, st *store.Store, actor store.User, id string) (store.Assignment, error) {
	a, err := st.GetAssignment(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Assignment{}, apperr.NotFound("作业不存在")
		}
		return store.Assignment{}, err
	}
	if a.ClassID != actor.ClassID {
		return store.Assignment{}, apperr.NotFound("作业不存在")
	}
	return a, nil
}
