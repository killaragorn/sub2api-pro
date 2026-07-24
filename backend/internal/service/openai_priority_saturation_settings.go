package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

// SettingKeyOpenAIPrioritySaturationEnabled independently enables the
// deterministic priority-saturation scheduler. It must not be conflated with
// the existing weighted advanced scheduler switch.
const SettingKeyOpenAIPrioritySaturationEnabled = "openai_priority_saturation_enabled"

func validateOpenAISchedulerSwitches(settings *SystemSettings) error {
	if settings.OpenAIAdvancedSchedulerEnabled && settings.OpenAIPrioritySaturationEnabled {
		return infraerrors.BadRequest(
			"OPENAI_SCHEDULER_SWITCH_CONFLICT",
			"openai_advanced_scheduler_enabled and openai_priority_saturation_enabled cannot both be enabled",
		)
	}
	return nil
}
