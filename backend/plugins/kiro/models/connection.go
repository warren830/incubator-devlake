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

package models

import (
	"strings"

	"github.com/apache/incubator-devlake/core/utils"
	helper "github.com/apache/incubator-devlake/helpers/pluginhelper/api"
)

// Default S3 prefixes. Kiro's console writes user activity reports and
// interaction logs under separate prefixes, and recommends (but does not
// require) separate buckets for the two.
const (
	DefaultReportPrefix    = "user-report"
	DefaultPromptLogPrefix = "logging"
)

// KiroConn holds the essential information to connect to the AWS S3 buckets
// that Kiro exports to.
type KiroConn struct {
	// AccessKeyId for AWS
	AccessKeyId string `mapstructure:"accessKeyId" json:"accessKeyId"`
	// SecretAccessKey for AWS
	SecretAccessKey string `mapstructure:"secretAccessKey" json:"secretAccessKey"`
	// Region of the buckets, and of the Kiro profile that writes to them
	Region string `mapstructure:"region" json:"region"`

	// Bucket holding the user activity report CSVs
	Bucket string `mapstructure:"bucket" json:"bucket"`
	// ReportPrefix within Bucket; defaults to DefaultReportPrefix when empty
	ReportPrefix string `mapstructure:"reportPrefix" json:"reportPrefix"`

	// PromptLogBucket holding the interaction logs. Kiro recommends a bucket
	// separate from the report one; when empty this falls back to Bucket so
	// that single-bucket and dual-bucket setups share one code path.
	PromptLogBucket string `mapstructure:"promptLogBucket" json:"promptLogBucket"`
	// PromptLogPrefix within PromptLogBucket; defaults to
	// DefaultPromptLogPrefix when empty
	PromptLogPrefix string `mapstructure:"promptLogPrefix" json:"promptLogPrefix"`

	// IdentityStoreId for AWS IAM Identity Center. Optional: it only resolves
	// display names. User identity is keyed on the User_Email column of the
	// report CSV, so collection works fully without it.
	IdentityStoreId string `mapstructure:"identityStoreId" json:"identityStoreId"`
	// IdentityStoreRegion may differ from the S3 region. Optional.
	IdentityStoreRegion string `mapstructure:"identityStoreRegion" json:"identityStoreRegion"`

	// RateLimitPerHour limits the requests sent to AWS
	RateLimitPerHour int `mapstructure:"rateLimitPerHour" json:"rateLimitPerHour"`
}

// GetReportPrefix returns the report prefix with its default applied.
func (conn *KiroConn) GetReportPrefix() string {
	if p := strings.Trim(strings.TrimSpace(conn.ReportPrefix), "/"); p != "" {
		return p
	}
	return DefaultReportPrefix
}

// GetPromptLogBucket returns the interaction log bucket, falling back to the
// report bucket when no separate one is configured.
func (conn *KiroConn) GetPromptLogBucket() string {
	if b := strings.TrimSpace(conn.PromptLogBucket); b != "" {
		return b
	}
	return strings.TrimSpace(conn.Bucket)
}

// GetPromptLogPrefix returns the interaction log prefix with its default
// applied.
func (conn *KiroConn) GetPromptLogPrefix() string {
	if p := strings.Trim(strings.TrimSpace(conn.PromptLogPrefix), "/"); p != "" {
		return p
	}
	return DefaultPromptLogPrefix
}

// UsesSeparateBuckets reports whether reports and logs live in different
// buckets, which determines whether both need an access check.
func (conn *KiroConn) UsesSeparateBuckets() bool {
	return conn.GetPromptLogBucket() != strings.TrimSpace(conn.Bucket)
}

func (conn *KiroConn) Sanitize() KiroConn {
	conn.SecretAccessKey = utils.SanitizeString(conn.SecretAccessKey)
	return *conn
}

// The three methods below satisfy plugin.ApiConnection, which the scope-browsing
// helpers require. They are vestigial here: this data source is S3 accessed
// through the AWS SDK, not an HTTP endpoint. Implementing them lets the plugin
// reuse the shared scope-discovery plumbing instead of hand-rolling equivalent
// endpoints.

// Value receivers, because the interface is satisfied by the value type.
//
// GetEndpoint returns the bucket as an s3:// URI. Nothing dials it; it exists so
// the shared helpers have a non-empty identifier to log.
func (conn KiroConn) GetEndpoint() string {
	return "s3://" + strings.TrimSpace(conn.Bucket) + "/"
}

// GetProxy returns empty: AWS SDK proxying is configured through the standard
// AWS environment variables, not per-connection.
func (conn KiroConn) GetProxy() string {
	return ""
}

func (conn KiroConn) GetRateLimitPerHour() int {
	return conn.RateLimitPerHour
}

// KiroConnection holds KiroConn plus ID/Name for database storage
type KiroConnection struct {
	helper.BaseConnection `mapstructure:",squash"`
	KiroConn              `mapstructure:",squash"`
}

func (KiroConnection) TableName() string {
	return "_tool_kiro_connections"
}

func (connection KiroConnection) Sanitize() KiroConnection {
	connection.KiroConn = connection.KiroConn.Sanitize()
	return connection
}

func (connection *KiroConnection) MergeFromRequest(target *KiroConnection, body map[string]interface{}) error {
	secretKey := target.SecretAccessKey
	if err := helper.DecodeMapStruct(body, target, true); err != nil {
		return err
	}
	modifiedSecretKey := target.SecretAccessKey
	if modifiedSecretKey == "" || modifiedSecretKey == utils.SanitizeString(secretKey) {
		target.SecretAccessKey = secretKey
	}
	return nil
}
