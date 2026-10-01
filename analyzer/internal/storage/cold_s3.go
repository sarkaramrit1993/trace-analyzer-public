package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
)

const interestingTracesPrefix = "interesting-traces"

// ColdTierStorage handles long-term storage in gzipped JSON on S3-compatible storage
type ColdTierStorage struct {
	client     *s3.Client
	bucket     string
	pathPrefix string
	mu         sync.RWMutex
}

// S3Config holds S3-compatible storage configuration
type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
}

// NewColdTierStorage creates a new S3-based cold storage
func NewColdTierStorage(cfg S3Config) (*ColdTierStorage, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               cfg.Endpoint,
				HostnameImmutable: true,
			}, nil
		},
	)

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey,
			cfg.SecretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	storage := &ColdTierStorage{
		client:     client,
		bucket:     cfg.Bucket,
		pathPrefix: "rollups",
	}

	log.Info().
		Str("endpoint", cfg.Endpoint).
		Str("bucket", cfg.Bucket).
		Msg("Cold tier storage initialized (S3)")

	return storage, nil
}

// WriteRollups writes a batch of rollups as gzipped JSON to S3
func (c *ColdTierStorage) WriteRollups(rollups []*models.Rollup) error {
	if len(rollups) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	byHour := make(map[string][]*models.Rollup)
	for _, r := range rollups {
		hourKey := r.WindowStart.Format("2006/01/02/15")
		byHour[hourKey] = append(byHour[hourKey], r)
	}

	for hourKey, hourRollups := range byHour {
		if err := c.writePartition(hourKey, hourRollups); err != nil {
			return fmt.Errorf("write partition %s: %w", hourKey, err)
		}
	}

	return nil
}

// WriteInterestingTraces writes anomaly/deviation traces as gzipped JSON to S3.
func (c *ColdTierStorage) WriteInterestingTraces(traces []*models.InterestingTrace) error {
	if len(traces) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	byHour := make(map[string][]*models.InterestingTrace)
	for _, t := range traces {
		hourKey := t.Timestamp.UTC().Format("2006/01/02/15")
		byHour[hourKey] = append(byHour[hourKey], t)
	}

	for hourKey, hourTraces := range byHour {
		if err := c.writeInterestingPartition(hourKey, hourTraces); err != nil {
			return fmt.Errorf("write interesting partition %s: %w", hourKey, err)
		}
	}

	return nil
}

func (c *ColdTierStorage) writePartition(hourKey string, rollups []*models.Rollup) error {
	data, err := json.Marshal(rollups)
	if err != nil {
		return fmt.Errorf("marshal rollups: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return fmt.Errorf("gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	key := path.Join(c.pathPrefix, hourKey, fmt.Sprintf("rollups_%d.json.gz", time.Now().UnixNano()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err = c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(c.bucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("application/gzip"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return fmt.Errorf("upload to S3: %w", err)
	}

	log.Info().
		Str("key", key).
		Int("rollups", len(rollups)).
		Int("bytes", buf.Len()).
		Msg("Wrote cold tier file")

	return nil
}

func (c *ColdTierStorage) writeInterestingPartition(hourKey string, traces []*models.InterestingTrace) error {
	data, err := json.Marshal(traces)
	if err != nil {
		return fmt.Errorf("marshal interesting traces: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return fmt.Errorf("gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	key := path.Join(interestingTracesPrefix, hourKey, fmt.Sprintf("interesting_%d.json.gz", time.Now().UnixNano()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err = c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(c.bucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentType:     aws.String("application/gzip"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return fmt.Errorf("upload to S3: %w", err)
	}

	log.Info().
		Str("key", key).
		Int("interesting_traces", len(traces)).
		Int("bytes", buf.Len()).
		Msg("Wrote cold tier interesting traces file")

	return nil
}

// ReadRollups reads rollups from S3 for a time range
func (c *ColdTierStorage) ReadRollups(serviceID string, start, end time.Time) ([]*models.Rollup, error) {
	// Don't hold lock during I/O - each operation is independent
	var rollups []*models.Rollup

	current := start.Truncate(time.Hour)
	for !current.After(end) {
		prefix := path.Join(c.pathPrefix, current.Format("2006/01/02/15"))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := c.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(c.bucket),
			Prefix: aws.String(prefix),
		})
		cancel()

		if err != nil {
			log.Warn().Err(err).Str("prefix", prefix).Msg("Failed to list objects")
			current = current.Add(time.Hour)
			continue
		}

		for _, obj := range resp.Contents {
			if obj.Key == nil {
				continue
			}
			fileRollups, err := c.readFile(*obj.Key, serviceID)
			if err != nil {
				log.Warn().Err(err).Str("key", *obj.Key).Msg("Failed to read file")
				continue
			}
			rollups = append(rollups, fileRollups...)
		}

		current = current.Add(time.Hour)
	}

	return rollups, nil
}

func (c *ColdTierStorage) readFile(key string, serviceIDFilter string) ([]*models.Rollup, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}

	var rollups []*models.Rollup
	if err := json.Unmarshal(data, &rollups); err != nil {
		return nil, err
	}

	if serviceIDFilter == "" {
		return rollups, nil
	}

	var filtered []*models.Rollup
	for _, r := range rollups {
		if r.ServiceID == serviceIDFilter {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// Stats returns storage statistics
func (c *ColdTierStorage) Stats() (objectCount int64, totalBytes int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, prefix := range []string{c.pathPrefix, interestingTracesPrefix} {
		paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(c.bucket),
			Prefix: aws.String(prefix),
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return 0, 0, err
			}
			for _, obj := range page.Contents {
				objectCount++
				if obj.Size != nil {
					totalBytes += *obj.Size
				}
			}
		}
	}

	return objectCount, totalBytes, nil
}
