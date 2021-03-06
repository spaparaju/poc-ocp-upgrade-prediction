# Proof of concept - Predict OCP cluster upgrade failures

[![Build Status](https://travis-ci.org/fabric8-analytics/poc-ocp-upgrade-prediction.svg?branch=master)](https://travis-ci.org/fabric8-analytics/poc-ocp-upgrade-prediction)

## Initial Setup and configuration

### Setting up a local graph instance
- Clone this repo OUTSIDE your gopath as with: `git clone --recursive -submodules -j8 https://github.com/fabric8-analytics/poc-ocp-upgrade-prediction`
- Run [Installation script](./scripts/install-graph.sh) to setup your gremlin (one time).
- Run [automation script](./scripts/run_graph.sh) to run your gremlin every time you want to bring up the graph to run this code.
- Now create JanusGraph indices for faster node creation, as with `cd scripts; go run populate_janusgraph_schema.go`
- Alternatively just give a remote gremlin instance to `GREMLIN_REST_URL`

#### Setting up the Graph to work from disk instead of in-memory
Inside the dynamodb subrepo, lives a pom.xml that we use to start dynamodb. Inside that file changes need to be made:

1) Remove the inMemory argument from everywhere
2) Add a "sharedDB" argument for dynamodb, with a path to a data folder, see sample change diff:

```xml
@@ -512,7 +512,6 @@
                                         <argument>-Djava.library.path=dynamodb/DynamoDBLocal_lib</argument>
                                         <argument>-jar</argument>
                                         <argument>dynamodb/DynamoDBLocal.jar</argument>
-                                        <argument>-inMemory</argument>
                                         <argument>-port</argument>
                                         <argument>${dynamodb-local.port}</argument>
                                         <argument>-sharedDb</argument>
@@ -625,10 +624,11 @@
                                         <argument>-Djava.library.path=${project.build.directory}/dynamodb/DynamoDBLocal_lib</argument>
                                         <argument>-jar</argument>
                                         <argument>${project.build.directory}/dynamodb/DynamoDBLocal.jar</argument>
-                                        <argument>-inMemory</argument>
                                         <argument>-port</argument>
                                         <argument>${dynamodb-local.port}</argument>
                                         <argument>-sharedDb</argument>
+                                       <argument>-dbPath</argument>
+                                       <argument>/Users/avgupta/data</argument>
                                     </arguments>
                                 </configuration>
                                 <goals>
@@ -641,4 +641,4 @@
```

### Environment Variables
- Make sure gremlin server is running.
- Need to set the following environment variables: 
```json5
            {
                "GREMLIN_REST_URL": "http://localhost:8182", // The API endpoint for the Gremlin server.
                "GOPATH": "GOPATH", // The Gopath on the current machine that you're working off of.
                "GH_TOKEN": "YOUR_GH_TOKEN", // Github token, should contain all repo permissions. This is required to fork the projects into a namespace for you
                "KUBECONFIG": "PATH_TO_KUBECONFIG", // Path to a kubeconfig, openshift-install binary should have generated this for you under "auth/" folder wherever you ran the cluster installation.
                "KUBERNETES_SERVICE_PORT": 6443, // Port on which your Kubernetes cluster API is running, this is generally 6443 AFAIK.
                "KUBERNETES_SERVICE_HOST": "PATH_TO_YOUR_DEV_CLUSTER_API", // Path to a running Kubernetes cluster that we need to run the end to end tests/service end to end tests. Is of the form api.*.devcluster.openshift.com
                "REMOTE_SERVER_URL": "", // This is the path to the layer running the origin end-to-end test wrapper.
 	            "AWS_SQS_QUEUE_NAME": "",  // Name of the queue where runtime call stacks are captured
                "AWS_SQS_REGION":     "", // AWS region where SQS queue is located
 		          "AWS_ACCESS_KEY_ID" : "",  // AWS access key
		          "AWS_SECRET_ACCESS_KEY": "", // AWS secret 
                "OPENSHIFT_CAPTURE_TRACE"= false, // Enable capturing runtime paths at SQS queues
                "OPENSHIFT_PRINT_TRACE_ON_CONSOLE"= "", // Enable capturing runtime paths at console
                "PATCH_SKIP_FOLDER_LIST_FILE"= "ignore-dirs-for-patching.txt", //Location of file containing list of directories for getting exlcuding from the patching process  
            }
```

### Build instructions

- You need a recent version of Go (1.12.x)
- You'll need to increase your git max buffer size: `git config http.postBuffer 524288000`
- For the python bits, Python 3.6 is required. You would additionally need `Flask` and `requests`. Install these with `pip install flask requests`
- This project uses the new vgo(go modules) dependency management system, simply running `make build` with fetch all the dependencies
- Install all the Go binaries with: `make install`

## Artifacts: Go

### Compile time flow creation: clustergraph
- First create the compile time paths using the clustergraph flow, if you you are using cluster version 4.1.9 and have origin located in ~/origin run clustergraph  as with: `$GOPATH/bin/clustergraph --cluster-version=4.0.0-0.ci-2019-04-23-214213 --gopath=~ ~/origin ~/origin/vendor/k8s.io/kubernetes/ `
- Make sure the index creation outlined in the final step of the [first phase](#setting-up-a-local-graph-instance) has been done otherwise this would be painfully slow.
- Currently, in order for this to work for just one service there's a `break` statement at the end of the control block [here](https://github.com/fabric8-analytics/poc-ocp-upgrade-prediction/blob/master/cmd/clustergraph/clustergraph.go#L67). Remove it to create the graph for the entire payload.

### Component end to end test node creation flow for a PR: api 
- This spins up a REST API as with: `$GOPATH/bin/api`
- Send a get request to the REST API, the PR and repo are hardcoded for now runtimepaths will be created. Here's a sample request:
```bash
curl -X GET \
  http://localhost:8080/api/v1/createprnode \
  -H 'Content-Type: application/json' \
  -H 'Postman-Token: b9c125ed-8d5f-4481-9251-1ee42d44a723' \
  -H 'cache-control: no-cache' \
  -d '{
    "pr_id": 482,
    "repo_url": "openshift/machine-config-operator/"
}'
```
### Patching source code to add imports/functions and prepend statements: patchsource
* The patchsource binary takes in a source directory and a yaml config which contains the imports to be added to each non-ignored package/file etc. and patches all the .go files in the source directory to modify them. 
Please refer to 'patchtemplate.yaml'. Patching involve transfer of all the runtime paths to AWS SQS queues for removing duplicate call stacks. 

* Sample config:
```yaml
imports:
  godefaultfmt: fmt
func_add: |
  func logStatement() {
    godefaultfmt.Println("Hello World.")
  }
prepend_body: |
  logStatement()
  defer logStatement()
```
The above yaml, when saved in a file called `source_config.yaml` and passed to the binary as with:
```bash
  $ patchsource --source-dir=[path_to_origin_dir]  --code-config-yaml=sources_config.yaml  # Excludes vendor, to include it see below.
```
will change all packages of the source pointed to by source dir to:
  - Add imports marked under imports with the name as the key and the importpath as the value to the function, i.e. `godefaultfmt: fmt` becomes `import godefaultfmt "fmt"` in the Go source code.
  - Prepends the lines in the `prepend_body` key to all the function declarations and the function literal declarations in the go files present in sourcedir.
  - Adds the function declared under `func_add` to all packages if some logic is required without adding in the overhead of a third party import.
  - This binary is generic and can be used to patch any golang source code.

  NOTE: `vendor` a special folder in golang that has vendored dependencies is ignored by default, to include it use the `--include-vendor` flag.
  ```bash
    $ patchsource --source-dir=[path_to_origin_dir] --code-config-yaml=sources_config.yaml --include-vendor  # Includes vendor.
  ```

### Payload creation for running the end to end tests: custompayload-creator

* Follow the installation procedure to install all the binaries from above
* Make sure to login to registry.svc.ci.openshift.org(your ~/.docker/config.json should have a token for registry.svc.ci.openshift.org)
* Optionally specify the `--no-images` flag so that it doesn't bother with docker image creation.
* Also need to supply your Github username (the username to which the repo is attached, otherwise we run into clone API limits.)
* This binary will create a custom payload based off an already existing payload for an OCP release. Sample usage:

```bash
$ $GOPATH/bin/custompayload-create --cluster-version=4.0.0-0.ci-2019-04-15-000954 --user-name='rootAvish' --destdir=/tmp --no-images # This version won't work, it's outdated. Pick one from the ocp releases page.
```
* This will, in you `destdir` or your current working directory create a directory inside which all the services will be cloned and patched.
* This'll take a long time.

### Patched openshift tests to run serially: openshift-tests

* Fork this, you need to clone: `https://github.com/rootavish/origin` (not included as a submodule here)
* Compile the binary, as with `make WHAT=cmd/openshift-tests`
* The binary would be available under `_output/bin/{linux/darwin}/amd64/` and can be run with `./openshift-tests`

## Artifacts: Python

### Fork all payload repositories to a namespace(org/user): scripts/github_forker.py

This script forks all the missing repositories that are mentioned in the payload to a namespace that you own, this is required for pushing changes to them so they can be picked up by the CI operator. Currently only works with the `--org` flag set and requires you to create an organization on Github. Sample usage:

```bash
$ python github_forker.py --namespace poc-ocp-upgrades --cluster-version=4.0.0-0.ci-2019-04-22-163416 --org=true
```
### Push all changes for tracing source code to fork: scripts/commit_sources.py

This script will commit all the changes made by `clusterpatcher` script and push them to github to our forks so that it can be picked by the ci-operator. Sample usage is almost the same as github_forker with the exception that `--no-verify=true` needs to be set if using global `git-secrets` as some repositories contain dummy AWS keys for running tests.
```bash
$python commit_sources.py --namespace poc-ocp-upgrades --cluster-version=4.0.0-0.ci-2019-04-22-163416 --org=true --no-verify=true
```

### Wrapper over gremlin for e2e product runtime node creation: e2e_logger_api.py

Run this with `python e2e_logger_api.py`, should start the flask development server at port `5001`.

## Current Limitations

* Does not parse dynamic function calls such as anonymous functions from a map because they are mapped at runtime and were too much work for POC [like _bindata here](https://github.com/openshift/machine-config-operator/blob/master/pkg/operator/assets/bindata.go#L1195).


## License and contributing

See [LICENSE](LICENSE), specific parts of the source are under more permissive licensing in keeping with the wishes of the original authors and those files with the appropriate attribution in their hears contain information about the same.

To contribute to this project Just send a PR.
