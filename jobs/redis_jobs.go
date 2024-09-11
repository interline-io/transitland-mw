package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	workers "github.com/digitalocean/go-workers2"
	"github.com/go-redis/redis/v8"

	"github.com/interline-io/log"
)

func init() {
	var _ JobQueue = &RedisJobs{}
}

// RedisJobs is a simple wrapper around go-workers
type RedisJobs struct {
	queuePrefix string
	producer    *workers.Producer
	manager     *workers.Manager
	client      *redis.Client
	jobMapper   *jobMapper
	middlewares []JobMiddleware
}

func NewRedisJobs(client *redis.Client, queuePrefix string) *RedisJobs {
	f := RedisJobs{
		queuePrefix: queuePrefix,
		client:      client,
		jobMapper:   newJobMapper(),
	}
	f.Use(newLog())
	return &f
}

func (f *RedisJobs) Use(mwf JobMiddleware) {
	f.middlewares = append(f.middlewares, mwf)
}

func (f *RedisJobs) AddQueue(queue string, count int) error {
	manager, err := f.getManager()
	if err != nil {
		return err
	}
	manager.AddWorker(f.queueName(queue), count, func(msg *workers.Msg) error {
		return f.processJob(queue, msg)
	})
	return nil
}

func (w *RedisJobs) AddJobType(jobFn JobFn) error {
	return w.jobMapper.AddJobType(jobFn)
}

func (f *RedisJobs) RunJob(ctx context.Context, job Job) error {
	w, err := f.jobMapper.GetRunner(job.JobType, job.JobArgs)
	if err != nil {
		return err
	}
	if w == nil {
		return errors.New("no job")
	}
	for _, mwf := range f.middlewares {
		w = mwf(w)
		if w == nil {
			return errors.New("no job")
		}
	}
	if err := w.Run(ctx, job); err != nil {
		log.Trace().Err(err).Msg("job failed")
	}
	return nil
}

func (f *RedisJobs) AddJob(job Job) error {
	if f.producer == nil {
		var err error
		f.producer, err = workers.NewProducerWithRedisClient(workers.Options{
			ProcessID: strconv.Itoa(os.Getpid()),
		}, f.client)
		if err != nil {
			return err
		}
	}
	if job.Unique {
		key, err := job.HexKey()
		if err != nil {
			return err
		}
		fullKey := fmt.Sprintf("queue:%s:unique:%s", f.queueName(job.Queue), key)
		deadlineTtl := time.Duration(60*60) * time.Second
		if sec := job.JobDeadline - time.Now().In(time.UTC).Unix(); sec > 0 {
			deadlineTtl = time.Duration(sec) * time.Second
		}
		logMsg := log.Trace().Interface("job", job).Str("key", fullKey).Float64("ttl", deadlineTtl.Seconds())
		if !f.client.SetNX(context.Background(), fullKey, "unique", deadlineTtl).Val() {
			logMsg.Msg("unique job already locked")
			return nil
		} else {
			logMsg.Msg("unique job locked")
		}
	}
	rjob := Job{
		JobType:     job.JobType,
		JobArgs:     job.JobArgs,
		Unique:      job.Unique,
		JobDeadline: job.JobDeadline,
	}
	_, err := f.producer.Enqueue(f.queueName(job.Queue), rjob.JobType, rjob)
	return err
}

func (f *RedisJobs) queueName(q string) string {
	if q == "" {
		q = "default"
	}
	return f.queuePrefix + q
}

func (f *RedisJobs) getManager() (*workers.Manager, error) {
	var err error
	if f.manager == nil {
		f.manager, err = workers.NewManagerWithRedisClient(workers.Options{
			ProcessID: strconv.Itoa(os.Getpid()),
		}, f.client)
	}
	return f.manager, err
}

func (f *RedisJobs) processJob(queueName string, msg *workers.Msg) error {
	j := msg.Args()
	job := Job{
		JobType: msg.Class(),
		jobId:   msg.Jid(),
		Queue:   queueName,
	}
	job.JobArgs, _ = j.Get("job_args").Map()
	job.JobDeadline, _ = j.Get("job_deadline").Int64()
	job.Unique, _ = j.Get("unique").Bool()
	now := time.Now().In(time.UTC).Unix()
	ctx := context.Background()
	if job.Unique {
		// Consider more advanced locking options
		key, err := job.HexKey()
		if err != nil {
			return err
		}
		fullKey := fmt.Sprintf("queue:%s:unique:%s", f.queueName(job.Queue), key)
		logMsg := log.Trace().Str("key", fullKey)
		defer func() {
			if result, err := f.client.Del(ctx, fullKey).Result(); err != nil {
				logMsg.Err(err).Msg("error unlocking job!")
			} else {
				logMsg.Int64("result", result).Msg("unique job unlocked")
			}
		}()
	}
	if job.JobDeadline > 0 && now > job.JobDeadline {
		log.Trace().Int64("job_deadline", job.JobDeadline).Int64("now", now).Msg("job skipped - deadline in past")
		return nil
	}
	return f.RunJob(ctx, job)
}

func (f *RedisJobs) Run() error {
	log.Infof("jobs: running")
	manager, err := f.getManager()
	if err == nil {
		// Blocks
		manager.Run()
	}
	return err
}

func (f *RedisJobs) Stop() error {
	log.Infof("jobs: stopping")
	manager, err := f.getManager()
	if err == nil {
		manager.Stop()
	}
	return err
}
