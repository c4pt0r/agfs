package s3fs

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	log "github.com/sirupsen/logrus"
)

// MultipartUpload represents an active multipart upload
type MultipartUpload struct {
	UploadID   string
	Key        string
	Bucket     string
	Parts      []types.CompletedPart
	PartNumber int32
}

// CreateMultipartUpload initiates a multipart upload
func (c *S3Client) CreateMultipartUpload(ctx context.Context, key string) (*MultipartUpload, error) {
	log.Debugf("[s3fs] Creating multipart upload: bucket=%s, key=%s", c.bucket, key)

	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}

	result, err := c.client.CreateMultipartUpload(ctx, input)
	if err != nil {
		log.Errorf("[s3fs] Failed to create multipart upload: %v", err)
		return nil, fmt.Errorf("failed to create multipart upload: %w", err)
	}

	upload := &MultipartUpload{
		UploadID:   *result.UploadId,
		Key:        key,
		Bucket:     c.bucket,
		Parts:      make([]types.CompletedPart, 0),
		PartNumber: 1,
	}

	log.Infof("[s3fs] Created multipart upload: upload_id=%s, key=%s", upload.UploadID, key)

	return upload, nil
}

// UploadPart uploads a single part
func (c *S3Client) UploadPart(ctx context.Context, upload *MultipartUpload, partNumber int32, data []byte) error {
	log.Debugf("[s3fs] Uploading part: upload_id=%s, part_number=%d, size=%d",
		upload.UploadID, partNumber, len(data))

	input := &s3.UploadPartInput{
		Bucket:     aws.String(upload.Bucket),
		Key:        aws.String(upload.Key),
		PartNumber: aws.Int32(partNumber),
		UploadId:   aws.String(upload.UploadID),
		Body:       bytes.NewReader(data),
	}

	result, err := c.client.UploadPart(ctx, input)
	if err != nil {
		log.Errorf("[s3fs] Failed to upload part %d: %v", partNumber, err)
		return fmt.Errorf("failed to upload part %d: %w", partNumber, err)
	}

	// Record completed part
	upload.Parts = append(upload.Parts, types.CompletedPart{
		ETag:       result.ETag,
		PartNumber: aws.Int32(partNumber),
	})

	log.Debugf("[s3fs] Part %d uploaded: etag=%s", partNumber, *result.ETag)

	return nil
}

// CompleteMultipartUpload completes the multipart upload
func (c *S3Client) CompleteMultipartUpload(ctx context.Context, upload *MultipartUpload) error {
	log.Debugf("[s3fs] Completing multipart upload: upload_id=%s, parts=%d",
		upload.UploadID, len(upload.Parts))

	// Sort parts by part number (required by S3)
	sort.Slice(upload.Parts, func(i, j int) bool {
		return *upload.Parts[i].PartNumber < *upload.Parts[j].PartNumber
	})

	input := &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(upload.Bucket),
		Key:      aws.String(upload.Key),
		UploadId: aws.String(upload.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: upload.Parts,
		},
	}

	result, err := c.client.CompleteMultipartUpload(ctx, input)
	if err != nil {
		log.Errorf("[s3fs] Failed to complete multipart upload: %v", err)
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	log.Infof("[s3fs] Multipart upload completed: key=%s, etag=%s",
		upload.Key, *result.ETag)

	return nil
}

// AbortMultipartUpload aborts an incomplete multipart upload
func (c *S3Client) AbortMultipartUpload(ctx context.Context, upload *MultipartUpload) error {
	log.Debugf("[s3fs] Aborting multipart upload: upload_id=%s", upload.UploadID)

	input := &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(upload.Bucket),
		Key:      aws.String(upload.Key),
		UploadId: aws.String(upload.UploadID),
	}

	_, err := c.client.AbortMultipartUpload(ctx, input)
	if err != nil {
		log.Errorf("[s3fs] Failed to abort multipart upload: %v", err)
		return fmt.Errorf("failed to abort multipart upload: %w", err)
	}

	log.Infof("[s3fs] Multipart upload aborted: upload_id=%s", upload.UploadID)

	return nil
}

// ListParts lists uploaded parts of a multipart upload
func (c *S3Client) ListParts(ctx context.Context, key, uploadID string) ([]types.Part, error) {
	input := &s3.ListPartsInput{
		Bucket:   aws.String(c.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	}

	result, err := c.client.ListParts(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list parts: %w", err)
	}

	return result.Parts, nil
}
