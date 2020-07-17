/*
 * Copyright 2020 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package subscription_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/dgraph-io/dgraph/graphql/e2e/common"
	"github.com/dgraph-io/dgraph/graphql/schema"
	"github.com/dgraph-io/dgraph/testutil"
	"github.com/stretchr/testify/require"
)

const (
	graphQLEndpoint      = "http://localhost:8180/graphql"
	subscriptionEndpoint = "ws://localhost:8180/graphql"
	adminEndpoint        = "http://localhost:8180/admin"
	sch                  = `
	type Product @withSubscription {
		productID: ID!
		name: String @search(by: [term])
		reviews: [Review] @hasInverse(field: about)
	}

	type Customer  {
		username: String! @id @search(by: [hash, regexp])
		reviews: [Review] @hasInverse(field: by)
	}

	type Review {
		id: ID!
		about: Product!
		by: Customer!
		comment: String @search(by: [fulltext])
		rating: Int @search
	}
	`
	schAuth = `
	type Todo @withSubscription @auth(
    	query: { rule: """
    		query ($USER: String!) {
    			queryTodo(filter: { owner: { eq: $USER } } ) {
    				__typename
    			}
   			}"""
     	}
   ){
        id: ID!
    	text: String! @search(by: [term])
     	owner: String! @search(by: [hash])
   }
# Dgraph.Authorization {"VerificationKey":"secret","Header":"Authorization","Namespace":"https://dgraph.io","Algo":"HS256"}
`
)

func TestSubscription(t *testing.T) {
	add := &common.GraphQLParams{
		Query: `mutation updateGQLSchema($sch: String!) {
			updateGQLSchema(input: { set: { schema: $sch }}) {
				gqlSchema {
					schema
				}
			}
		}`,
		Variables: map[string]interface{}{"sch": sch},
	}
	addResult := add.ExecuteAsPost(t, adminEndpoint)
	require.Nil(t, addResult.Errors)

	add = &common.GraphQLParams{
		Query: `mutation {
			addProduct(input: [
			  { name: "sanitizer"}
			]) {
			  product {
				productID
				name
			  }
			}
		  }`,
	}
	addResult = add.ExecuteAsPost(t, graphQLEndpoint)
	require.Nil(t, addResult.Errors)

	subscriptionClient, err := common.NewGraphQLSubscription(subscriptionEndpoint, &schema.Request{
		Query: `subscription{
			getProduct(productID: "0x2"){
			  name
			}
		  }`,
	}, `{}`)
	require.Nil(t, err)

	res, err := subscriptionClient.RecvMsg()
	require.NoError(t, err)

	touchedUidskey := "touched_uids"
	var subscriptionResp common.GraphQLResponse
	err = json.Unmarshal(res, &subscriptionResp)
	require.NoError(t, err)
	require.Nil(t, subscriptionResp.Errors)

	require.JSONEq(t, `{"getProduct":{"name":"sanitizer"}}`, string(subscriptionResp.Data))
	require.Contains(t, subscriptionResp.Extensions, touchedUidskey)
	require.Greater(t, int(subscriptionResp.Extensions[touchedUidskey].(float64)), 0)

	// Background indexing is happening so wait till it get indexed.
	time.Sleep(time.Second * 2)

	// Update the product to get the latest update.
	add = &common.GraphQLParams{
		Query: `mutation{
			updateProduct(input:{filter:{name:{allofterms:"sanitizer"}}, set:{name:"mask"}},){
			  product{
				name
			  }
			}
		  }
		  `,
	}
	addResult = add.ExecuteAsPost(t, graphQLEndpoint)
	require.Nil(t, addResult.Errors)

	res, err = subscriptionClient.RecvMsg()
	require.NoError(t, err)

	// makes sure that the we have a fresh instance to unmarshal to, otherwise there may be things
	// from the previous unmarshal
	subscriptionResp = common.GraphQLResponse{}
	err = json.Unmarshal(res, &subscriptionResp)
	require.NoError(t, err)
	require.Nil(t, subscriptionResp.Errors)

	// Check the latest update.
	require.JSONEq(t, `{"getProduct":{"name":"mask"}}`, string(subscriptionResp.Data))
	require.Contains(t, subscriptionResp.Extensions, touchedUidskey)
	require.Greater(t, int(subscriptionResp.Extensions[touchedUidskey].(float64)), 0)

	time.Sleep(2 * time.Second)
	// Change schema to terminate subscription..
	add = &common.GraphQLParams{
		Query: `mutation updateGQLSchema($sch: String!) {
			updateGQLSchema(input: { set: { schema: $sch }}) {
				gqlSchema {
					schema
				}
			}
		}`,
		Variables: map[string]interface{}{"sch": sch},
	}
	addResult = add.ExecuteAsPost(t, adminEndpoint)
	require.Nil(t, addResult.Errors)

	res, err = subscriptionClient.RecvMsg()
	require.NoError(t, err)

	require.Nil(t, res)
}

func TestSubscriptionAuth(t *testing.T) {
	add := &common.GraphQLParams{
		Query: `mutation updateGQLSchema($sch: String!) {
			updateGQLSchema(input: { set: { schema: $sch }}) {
				gqlSchema {
					schema
				}
			}
		}`,
		Variables: map[string]interface{}{"sch": schAuth},
	}
	addResult := add.ExecuteAsPost(t, adminEndpoint)
	require.Nil(t, addResult.Errors)
	time.Sleep(time.Second * 2)

	metaInfo := &testutil.AuthMeta{
		PublicKey: "secret",
		Namespace: "https://dgraph.io",
		Algo:      "HS256",
		Header:    "Authorization",
	}
	metaInfo.AuthVars = map[string]interface{}{
		"USER": "jatin",
		"ROLE": "USER",
	}

	add = &common.GraphQLParams{
		Query: `mutation{
              addTodo(input: [
                 {text : "GraphQL is exciting!!",
                  owner : "jatin"}
               ])
             {
               todo{
                    text
                    owner
               }
           }
         }`,
	}

	addResult = add.ExecuteAsPost(t, graphQLEndpoint)
	require.Nil(t, addResult.Errors)

	jwtTokens, err := metaInfo.GetSignedToken("secret")
	require.NoError(t, err)
	payload := fmt.Sprintf("{\"Authorization\": \"%s\"}", jwtTokens)
	subscriptionClient, err := common.NewGraphQLSubscription(subscriptionEndpoint, &schema.Request{
		Query: `subscription{
			    queryTodo{
                  owner
                  text
			}
		  }`,
	}, payload)
	require.Nil(t, err)

	time.Sleep(time.Second * 2)

	res, err := subscriptionClient.RecvMsg()
	require.NoError(t, err)
	//
	var subscriptionResp common.GraphQLResponse
	err = json.Unmarshal(res, &subscriptionResp)
	require.NoError(t, err)
	//require.Nil(t, subscriptionResp.Errors)
	//
	//require.JSONEq(t, `{"queryTodo": [
	//  {
	//    "owner": "jatin",
	//    "text": "GraphQL is exciting!!"
	//  }]}`, string(subscriptionResp.Data))
	//touchedUidskey := "touched_uids"
	//require.Contains(t, subscriptionResp.Extensions, touchedUidskey)
	//require.Greater(t, int(subscriptionResp.Extensions[touchedUidskey].(float64)), 0)
	//time.Sleep(time.Second * 20)
	// Background indexing is happening so wait till it get indexed.
	//time.Sleep(time.Second * 12)
	//
	//// Update the product to get the latest update.
	//add = &common.GraphQLParams{
	//	Query: `mutation{
	//          addTodo(input: [
	//             {text : "Dgraph is awesome!!",
	//              owner : "jatin"}
	//           ])
	//         {
	//           todo{
	//                text
	//                owner
	//           }
	//       }
	//     }
	//	  `,
	//}
	//addResult = add.ExecuteAsPost(t, graphQLEndpoint)
	//require.Nil(t, addResult.Errors)
	//
	//res, err = subscriptionClient.RecvMsg()
	//require.NoError(t, err)
	//err = json.Unmarshal(res, &subscriptionResp)
	//require.Error(t, err)
	//require.Nil(t, subscriptionResp.Errors)

	//require.JSONEq(t, `{"queryTodo": [
	//  {
	//    "owner": "jatin",
	//    "text": "GraphQL is exciting!!"
	//  },
	// {
	//    "owner" : "jatin",
	//    "text" : "Dgraph is awesome!!"
	// }]}`, string(subscriptionResp.Data))
	//require.Contains(t, subscriptionResp.Extensions, touchedUidskey)
	//require.Greater(t, int(subscriptionResp.Extensions[touchedUidskey].(float64)), 0)

	//// makes sure that the we have a fresh instance to unmarshal to, otherwise there may be things
	//// from the previous unmarshal
	//subscriptionResp = common.GraphQLResponse{}
	//err = json.Unmarshal(res, &subscriptionResp)
	//require.NoError(t, err)
	//require.Nil(t, subscriptionResp.Errors)
	//
	//// Check the latest update.
	//require.JSONEq(t, `{"getProduct":{"name":"mask"}}`, string(subscriptionResp.Data))
	//require.Contains(t, subscriptionResp.Extensions, touchedUidskey)
	//require.Greater(t, int(subscriptionResp.Extensions[touchedUidskey].(float64)), 0)
	//
	//time.Sleep(2 * time.Second)
	//// Change schema to terminate subscription..
	//add = &common.GraphQLParams{
	//	Query: `mutation updateGQLSchema($sch: String!) {
	//		updateGQLSchema(input: { set: { schema: $sch }}) {
	//			gqlSchema {
	//				schema
	//			}
	//		}
	//	}`,
	//	Variables: map[string]interface{}{"sch": sch},
	//}
	//addResult = add.ExecuteAsPost(t, adminEndpoint)
	//require.Nil(t, addResult.Errors)
	//
	//res, err = subscriptionClient.RecvMsg()
	//require.NoError(t, err)
	//
	//require.Nil(t, res)
}
