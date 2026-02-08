package entity

const (
	EmailStatusNew              int16 = 0
	EmailStatusProcessing       int16 = 1
	EmailStatusSuccess          int16 = 10
	EmailStatusTemporaryFailure int16 = 40
	EmailStatusUnknownFailure   int16 = 49
	EmailStatusPermanentFailure int16 = 50
)

type EmailHistory struct {
	RequestID string
	Recipient string
	Subject   string
	Content   string
	Status    int16
	Retries   int
}
