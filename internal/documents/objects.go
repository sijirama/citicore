package documents

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Objects struct {
    Client *s3.Client
    Bucket string
}

func NewObjects(ctx context.Context) (*Objects, error) {
    endpoint := os.Getenv("R2_ENDPOINT")
    bucket := os.Getenv("R2_BUCKET_NAME")
    access := os.Getenv("R2_ACCESS_KEY_ID")
    secret := os.Getenv("R2_SECRET_ACCESS_KEY")
    if endpoint == "" || bucket == "" || access == "" || secret == "" {
        return nil, fmt.Errorf("R2 object storage is not configured")
    }
    region := os.Getenv("R2_REGION")
    if region == "" { region = "auto" }
    cfg, err := config.LoadDefaultConfig(ctx,
        config.WithRegion(region),
        config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, "")))
    if err != nil { return nil, err }
    client := s3.NewFromConfig(cfg, func(options *s3.Options) {
        options.BaseEndpoint = aws.String(strings.TrimRight(endpoint, "/"))
        options.UsePathStyle = true
    })
    return &Objects{Client: client, Bucket: bucket}, nil
}

func (o *Objects) Put(ctx context.Context, key, contentType string, data []byte) error {
    _, err := o.Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(o.Bucket), Key: aws.String(key),
        ContentType: aws.String(contentType), Body: bytes.NewReader(data),
    })
    return err
}

func (o *Objects) Get(ctx context.Context, key string) (io.ReadCloser, error) {
    result, err := o.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(key)})
    if err != nil { return nil, err }
    return result.Body, nil
}

func (o *Objects) Delete(ctx context.Context, key string) error {
    _, err := o.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(key)})
    return err
}
