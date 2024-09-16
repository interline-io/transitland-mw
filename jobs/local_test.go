package jobs

import (
	"testing"
)

func TestLocalJobs(t *testing.T) {
	newQueue := func(queueName string) JobQueue {
		q := NewLocalJobs()
		q.Use(newLog())
		q.AddQueue("default", 4)
		return q
	}
	testJobQueue(t, newQueue)
}
