package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Progress is temporary ingestion state. Completed documents have no progress key.
type Progress struct{ Redis *redis.Client }

func NewProgress(redisURL string) (*Progress, error) {
	if redisURL == "" {
		redisURL = "redis://localhost:6382"
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &Progress{Redis: redis.NewClient(options)}, nil
}
func progressKey(id uuid.UUID) string { return "citizen:ingest:" + id.String() }
func (p *Progress) Close() error      { return p.Redis.Close() }
func (p *Progress) Steps(ctx context.Context, id uuid.UUID) ([]Step, error) {
	values, err := p.Redis.HGetAll(ctx, progressKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	steps := make([]Step, 0, len(Steps))
	for _, name := range Steps {
		step := Step{Name: name, Status: "pending"}
		if value, ok := values[name]; ok {
			if err := json.Unmarshal([]byte(value), &step); err != nil {
				return nil, fmt.Errorf("decode %s progress: %w", name, err)
			}
		}
		steps = append(steps, step)
	}
	return steps, nil
}
func (p *Progress) Step(ctx context.Context, id uuid.UUID, name string) (Step, error) {
	step := Step{Name: name, Status: "pending"}
	value, err := p.Redis.HGet(ctx, progressKey(id), name).Result()
	if err == redis.Nil {
		return step, nil
	}
	if err != nil {
		return step, err
	}
	err = json.Unmarshal([]byte(value), &step)
	return step, err
}
func (p *Progress) save(ctx context.Context, id uuid.UUID, step Step) error {
	data, err := json.Marshal(step)
	if err != nil {
		return err
	}
	pipe := p.Redis.TxPipeline()
	pipe.HSet(ctx, progressKey(id), step.Name, data)
	pipe.Expire(ctx, progressKey(id), 7*24*time.Hour)
	_, err = pipe.Exec(ctx)
	return err
}
func (p *Progress) Start(ctx context.Context, id uuid.UUID, name string) error {
	step, err := p.Step(ctx, id, name)
	if err != nil {
		return err
	}
	now := time.Now()
	step.Status = "running"
	step.Attempts++
	step.Error = ""
	step.StartedAt = &now
	step.FinishedAt = nil
	return p.save(ctx, id, step)
}
func (p *Progress) Finish(ctx context.Context, id uuid.UUID, name string, processErr error) error {
	step, err := p.Step(ctx, id, name)
	if err != nil {
		return err
	}
	now := time.Now()
	step.FinishedAt = &now
	step.Status = "succeeded"
	step.Error = ""
	if processErr != nil {
		step.Status = "failed"
		step.Error = processErr.Error()
		if len(step.Error) > 1000 {
			step.Error = step.Error[:1000]
		}
	}
	return p.save(ctx, id, step)
}
func (p *Progress) ResetFrom(ctx context.Context, id uuid.UUID, name string) error {
	index := -1
	for i, step := range Steps {
		if step == name {
			index = i
		}
	}
	if index < 0 {
		return fmt.Errorf("unknown step %q", name)
	}
	fields := make([]string, 0, len(Steps)-index)
	for _, step := range Steps[index:] {
		fields = append(fields, step)
	}
	return p.Redis.HDel(ctx, progressKey(id), fields...).Err()
}
func (p *Progress) Clear(ctx context.Context, id uuid.UUID) error {
	return p.Redis.Del(ctx, progressKey(id)).Err()
}
