package worker

import (
	"context"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// stubStore implements MessageStore with zero-value behaviour. Specialised test
// stores embed it and override only the methods they exercise.
type stubStore struct{}

func (stubStore) Claim(context.Context, string, string, time.Time) (bool, error) { return false, nil }
func (stubStore) FindByID(context.Context, string) (*entity.Message, error)       { return nil, nil }
func (stubStore) LoadRecipients(context.Context, string) ([]*entity.MessageRecipient, error) {
	return nil, nil
}
func (stubStore) LoadAttachments(context.Context, string) ([]*entity.MessageAttachment, error) {
	return nil, nil
}
func (stubStore) MarkSent(context.Context, string, string, string, time.Time) error   { return nil }
func (stubStore) MarkRetry(context.Context, string, string, string, time.Time, time.Time) error {
	return nil
}
func (stubStore) MarkFailed(context.Context, string, string, string, time.Time) error { return nil }
func (stubStore) UpdateRecipientsStatus(context.Context, string, string, string) (int64, error) {
	return 0, nil
}
func (stubStore) FindDueQueued(context.Context, time.Time, int, []string) ([]string, error) {
	return nil, nil
}
func (stubStore) UnstickStaleSending(context.Context, time.Time, time.Time) (int64, error) {
	return 0, nil
}
