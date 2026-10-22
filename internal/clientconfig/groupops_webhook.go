package clientconfig

import "os"

// This existing signed webhook identity is separate from human/machine login.
type GroupOpsWebhookEnvironment struct{ Secret, Origin string }

func LoadGroupOpsWebhookEnvironment() GroupOpsWebhookEnvironment {
	return GroupOpsWebhookEnvironment{Secret: os.Getenv("AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET"), Origin: os.Getenv("AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN")}
}
