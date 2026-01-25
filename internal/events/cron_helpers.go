package events

import (
	"encoding/json"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/robfig/cron/v3"
)

// parseSchedule parses the cron expression
func (s *CronScheduler) parseSchedule(spec, timezone string) (cron.Schedule, error) {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	if timezone != "" {
		spec = "CRON_TZ=" + timezone + " " + spec
	}

	return parser.Parse(spec)
}

// buildCronPayload builds the payload for the cron event
func (s *CronScheduler) buildCronPayload(trigger *domain.CronTrigger) []byte {
	payload := map[string]interface{}{
		"trigger_id": trigger.ID,
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	bytes, _ := json.Marshal(payload)
	return bytes
}
