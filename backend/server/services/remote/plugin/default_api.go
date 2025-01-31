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

package plugin

import (
	"github.com/apache/incubator-devlake/core/models"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/server/services/remote/bridge"
)

func GetDefaultAPI(
	invoker bridge.Invoker,
	connType *models.DynamicTabler,
	txRuleType *models.DynamicTabler,
	helper *api.ConnectionApiHelper) map[string]map[string]plugin.ApiResourceHandler {
	connectionApi := &ConnectionAPI{
		invoker:  invoker,
		connType: connType,
		helper:   helper,
	}

	scopeApi := &ScopeAPI{
		txRuleType: txRuleType,
	}
	txruleApi := &TransformationRuleAPI{
		txRuleType: txRuleType,
	}

	return map[string]map[string]plugin.ApiResourceHandler{
		"test": {
			"POST": connectionApi.TestConnection,
		},
		"connections": {
			"POST": connectionApi.PostConnections,
			"GET":  connectionApi.ListConnections,
		},
		"connections/:connectionId": {
			"GET":    connectionApi.GetConnection,
			"PATCH":  connectionApi.PatchConnection,
			"DELETE": connectionApi.DeleteConnection,
		},
		"connections/:connectionId/scopes": {
			"PUT": scopeApi.PutScope,
			"GET": scopeApi.ListScopes,
		},
		"connections/:connectionId/scopes/*scopeId": {
			"GET":   scopeApi.GetScope,
			"PATCH": scopeApi.PatchScope,
		},
		"transformation_rules": {
			"POST": txruleApi.PostTransformationRules,
			"GET":  txruleApi.ListTransformationRules,
		},
		"transformation_rules/:id": {
			"GET":   txruleApi.GetTransformationRule,
			"PATCH": txruleApi.PatchTransformationRule,
		},
	}
}
