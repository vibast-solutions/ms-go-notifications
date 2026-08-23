package sns

// TopicAllowList is the set of SNS topic ARNs the webhook accepts. It is built
// from SNS_ALLOWED_TOPIC_ARNS; an empty list allows nothing (fail closed), so a
// misconfigured deployment rejects every message rather than trusting all.
type TopicAllowList map[string]bool

// NewTopicAllowList builds an allow-list from the configured ARNs.
func NewTopicAllowList(arns []string) TopicAllowList {
	set := make(TopicAllowList, len(arns))
	for _, a := range arns {
		if a != "" {
			set[a] = true
		}
	}
	return set
}

// Allows reports whether arn is permitted. Matching is exact (ARN resource
// parts are case-sensitive).
func (a TopicAllowList) Allows(arn string) bool {
	return a[arn]
}
