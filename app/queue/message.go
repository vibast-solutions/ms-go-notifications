package queue

const StreamName = "notifications:email:send-raw"
const ConsumerGroup = "email-consumers"

type EmailMessage struct {
	RequestID string
	Recipient string
	Subject   string
	Content   string
}
