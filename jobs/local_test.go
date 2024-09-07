package jobs

import (
	"testing"
)

func TestLocalJobs(t *testing.T) {
	newQueue := func(queueName string) JobQueue {
		q := NewLocalJobs()
		q.Use(newLog())
		return q
	}
	testJobQueue(t, newQueue)
}
