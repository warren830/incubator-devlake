/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tasks

import (
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/plugins/kiro/models"
)

// S3API is the subset of the S3 API this plugin uses, declared as an interface
// so collectors and extractors can be tested without AWS.
type S3API interface {
	ListObjectsV2(input *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error)
	GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)
}

// KiroS3Client is bound to exactly one bucket.
//
// Kiro recommends keeping interaction logs in a bucket separate from the
// activity reports, and the two may carry different KMS keys and IAM
// conditions. One client per bucket keeps those permission boundaries distinct,
// so an access failure points at a specific bucket instead of an ambiguous
// request.
type KiroS3Client struct {
	S3     S3API
	Bucket string
}

// KiroS3Clients holds the report and log clients for a connection. When no
// separate log bucket is configured both fields address the same bucket, so
// single-bucket and dual-bucket setups take the same code path everywhere else.
type KiroS3Clients struct {
	Report    *KiroS3Client
	PromptLog *KiroS3Client
}

// NewKiroS3Clients builds the client pair for a connection.
func NewKiroS3Clients(connection *models.KiroConnection) (*KiroS3Clients, errors.Error) {
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(connection.Region),
		Credentials: credentials.NewStaticCredentials(connection.AccessKeyId, connection.SecretAccessKey, ""),
	})
	if err != nil {
		return nil, errors.Convert(err)
	}

	// A single S3 service client can address both buckets; the split is at the
	// KiroS3Client level, which pins the bucket name.
	svc := s3.New(sess)

	return &KiroS3Clients{
		Report:    &KiroS3Client{S3: svc, Bucket: connection.Bucket},
		PromptLog: &KiroS3Client{S3: svc, Bucket: connection.GetPromptLogBucket()},
	}, nil
}

// ForFileType returns the client that owns a given file type.
func (c *KiroS3Clients) ForFileType(fileType string) *KiroS3Client {
	if fileType == models.FileTypeReport {
		return c.Report
	}
	return c.PromptLog
}

// Buckets returns the distinct buckets in use - one entry when reports and logs
// share a bucket, two when they do not.
func (c *KiroS3Clients) Buckets() []string {
	if c.Report.Bucket == c.PromptLog.Bucket {
		return []string{c.Report.Bucket}
	}
	return []string{c.Report.Bucket, c.PromptLog.Bucket}
}

// GetObjectBytes downloads an object in full.
//
// Objects are small - roughly 700 bytes for a chat log, a few KB for a
// completion log, and under 1 KB for a report CSV - so streaming would add
// complexity without saving memory.
func (c *KiroS3Client) GetObjectBytes(key string) ([]byte, errors.Error) {
	output, err := c.S3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, errors.Convert(err)
	}
	defer output.Body.Close()

	data, readErr := io.ReadAll(output.Body)
	if readErr != nil {
		return nil, errors.Convert(readErr)
	}
	return data, nil
}
