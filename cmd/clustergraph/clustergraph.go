package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	flag "github.com/spf13/pflag"

	"go.uber.org/zap"

	"github.com/fabric8-analytics/poc-ocp-upgrade-prediction/pkg/gremlin"
	"github.com/fabric8-analytics/poc-ocp-upgrade-prediction/pkg/serviceparser"
	"github.com/fabric8-analytics/poc-ocp-upgrade-prediction/pkg/utils"
	"github.com/tidwall/gjson"
)

var logger, _ = zap.NewDevelopment()
var sugarLogger = logger.Sugar()

func main() {
	clusterversion := flag.String("cluster-version", "", "A release version of OCP")
	destdir := flag.String("destdir", "./", "A folder where we can clone the repos of the service for analysis")

	flag.Parse()
	fmt.Println(flag.Args())
	payloadInfo, err := exec.Command("oc", "adm", "release", "info", "--commits=true",
		fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s", *clusterversion), "-o", "json").CombinedOutput()
	if err != nil {
		sugarLogger.Errorf("(%v): %s", err, string(payloadInfo))
	}

	clusterInfo := string(payloadInfo)
	services := gjson.Get(clusterInfo, "references.spec.tags").Array()
	clusterVersion := gjson.Get(clusterInfo, "digest").String()
	sugarLogger.Infow("Cluster version is", "clusterVersion", clusterVersion)

	gremlin.CreateClusterVerisonNode(clusterVersion)

	if len(flag.Args()) > 0 {
		for _, path := range flag.Args() {
			var serviceName string
			// Hardcoded for kube
			if strings.HasSuffix(path, "vendor/k8s.io/kubernetes") {
				serviceName = "hyperkube"
			} else {
				serviceName = ServicePackageMap[filepath.Base(path)]
			}
			components := serviceparser.NewServiceComponents(serviceName)
			serviceVersion := utils.GetServiceVersion(path)
			gremlin.CreateNewServiceVersionNode(clusterVersion, serviceName, serviceVersion)

			// Add the imports, packages, functions to graph.
			components.ParseService(serviceName, path)
			gremlin.AddPackageFunctionNodesToGraph(serviceName, serviceVersion, components)
			parseImportPushGremlin(serviceName, serviceVersion, components)

			// Hardcoding for now
			homedir, err := os.UserHomeDir()
			if err != nil {
				sugarLogger.Errorf("Got error: %v\n", err)
			}
			gopathCompilePaths := filepath.Join(homedir, "temp")
			edges, err := serviceparser.GetCompileTimeCalls(path, []string{"./cmd/" + serviceName}, gopathCompilePaths)
			if err != nil {
				sugarLogger.Errorf("Got error: %v, cannot build graph for %s", err, serviceName)
			}
			// Now create the compile time paths
			gremlin.CreateCompileTimePaths(edges, serviceName, serviceVersion)
		}
	} else {
		for idx := range services {
			service := services[idx].Map()
			serviceName := service["name"].String()
			sugarLogger.Info("Parsing service ", serviceName)
			serviceDetails := service["annotations"].Map()
			serviceVersion := serviceDetails["io.openshift.build.commit.id"].String()

			gremlin.CreateNewServiceVersionNode(clusterVersion, serviceName, serviceVersion)

			// Git clone the repo
			serviceRoot, cloned := utils.RunCloneShell(serviceDetails["io.openshift.build.source-location"].String(), *destdir+strings.Split(clusterVersion, ":")[1][:7],
				serviceDetails["io.openshift.build.commit.ref"].String(), serviceDetails["io.openshift.build.commit.id"].String())

			if cloned == false {
				continue
			}
			components := serviceparser.NewServiceComponents(serviceName)
			components.ParseService(serviceName, serviceRoot)
			gremlin.AddPackageFunctionNodesToGraph(serviceName, serviceVersion, components)
			parseImportPushGremlin(serviceName, serviceVersion, components)
			// TODO: This flow is broken at this point, fix in favor of CompileTimeFlows.
			break
			// This concludes the offline flow.
		}
	}
}

func filterImports(imports []serviceparser.ImportContainer, serviceName string) []serviceparser.ImportContainer {
	var filtered []serviceparser.ImportContainer
	unique := make(map[string]bool)
	for _, imported := range imports {
		if len(strings.Split(imported.ImportPath, "/")) > 2 && !strings.Contains(imported.ImportPath, serviceName) {
			if !unique[imported.ImportPath] {
				filtered = append(filtered, imported)
				unique[imported.ImportPath] = true
			}
		}
	}
	return filtered
}

func parseImportPushGremlin(serviceName, serviceVersion string, components *serviceparser.ServiceComponents) {
	serviceImports := components.AllPkgImports
	for _, imports := range serviceImports {
		imported, ok := imports.([]serviceparser.ImportContainer)
		if !ok {
			sugarLogger.Errorf("Imports are of wrong type: %T\n", imported)
		}
		imported = filterImports(imported, serviceName)
		gremlin.CreateDependencyNodes(serviceName, serviceVersion, imported)
	}
}
