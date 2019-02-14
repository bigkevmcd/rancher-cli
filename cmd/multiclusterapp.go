package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rancher/cli/cliclient"
	"github.com/rancher/norman/types"
	managementClient "github.com/rancher/types/client/management/v3"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	installMultiClusterAppDescription = `
Install a multi-cluster app in the current Rancher server. This defaults to the newest version of the app template.
Specify a version using '--version' if required.
					
Example:
	# Install the redis template with no other options
	$ rancher multiclusterapp install redis appFoo

	# Install the redis template and specify an answers file location
	$ rancher multiclusterapp install --answers /example/answers.yaml redis appFoo

	# Install the redis template and set multiple answers and the version to install
	$ rancher multiclusterapp install --set foo=bar --set baz=bunk --version 1.0.1 redis appFoo

	# Install the redis template and set target projects to install
	$ rancher multiclusterapp install --target mycluster:Default --target c-98pjr:p-w6c5f redis appFoo
`
)

type MultiClusterAppData struct {
	App     managementClient.MultiClusterApp
	Version string
	Targets string
}

func MultiClusterAppCommand() cli.Command {
	appLsFlags := []cli.Flag{
		formatFlag,
		cli.BoolFlag{
			Name:  "quiet,q",
			Usage: "Only display IDs",
		},
	}

	return cli.Command{
		Name:    "multiclusterapps",
		Aliases: []string{"multiclusterapp", "mcapps", "mcapp"},
		Usage:   "Operations with multi-cluster apps",
		Action:  defaultAction(multiClusterAppLs),
		Flags:   appLsFlags,
		Subcommands: []cli.Command{
			cli.Command{
				Name:        "ls",
				Usage:       "List multi-cluster apps",
				Description: "\nList all multi-cluster apps in the current Rancher server",
				ArgsUsage:   "None",
				Action:      multiClusterAppLs,
				Flags:       appLsFlags,
			},
			cli.Command{
				Name:      "delete",
				Usage:     "Delete a multi-cluster app",
				Action:    multiClusterAppDelete,
				ArgsUsage: "[APP_NAME]",
			},
			cli.Command{
				Name:        "install",
				Usage:       "Install a multi-cluster app",
				Description: installMultiClusterAppDescription,
				Action:      multiClusterAppTemplateInstall,
				ArgsUsage:   "[TEMPLATE_NAME, APP_NAME]...",
				Flags: []cli.Flag{
					cli.StringFlag{
						Name:  "answers,a",
						Usage: "Path to an answers file, the format of the file is a map with key:value. This supports JSON and YAML.",
					},
					cli.StringFlag{
						Name:  "values",
						Usage: "Path to a helm values file.",
					},
					cli.StringSliceFlag{
						Name:  "set",
						Usage: "Set answers for the template, can be used multiple times. Example: --set foo=bar",
					},
					cli.StringFlag{
						Name:  "version",
						Usage: "Version of the template to use",
					},
					cli.BoolFlag{
						Name:  "no-prompt",
						Usage: "Suppress asking questions and use the default values when required answers are not provided",
					},
					cli.StringSliceFlag{
						Name:  "target,t",
						Usage: "Target project names/ids to install the app into",
					},
					cli.IntFlag{
						Name:  "timeout",
						Usage: "Time in seconds to wait until the app is in a ready state",
						Value: 60,
					},
				},
			},
			cli.Command{
				Name:      "rollback",
				Usage:     "Rollback a multi-cluster app to a previous version",
				Action:    multiClusterAppRollback,
				ArgsUsage: "[APP_NAME/APP_ID, REVISION_ID/REVISION_NAME]",
				Flags: []cli.Flag{
					cli.BoolFlag{
						Name:  "show-revisions,r",
						Usage: "Show revisions available to rollback to",
					},
				},
			},
			cli.Command{
				Name:      "upgrade",
				Usage:     "Upgrade an app to a newer version",
				Action:    multiClusterAppUpgrade,
				ArgsUsage: "[APP_NAME/APP_ID VERSION]",
				Flags: []cli.Flag{
					cli.StringFlag{
						Name:  "answers,a",
						Usage: "Path to an answers file, the format of the file is a map with key:value. Supports JSON and YAML",
					},
					cli.StringFlag{
						Name:  "values",
						Usage: "Path to a helm values file.",
					},
					cli.StringSliceFlag{
						Name:  "set",
						Usage: "Set answers for the template, can be used multiple times. Example: --set foo=bar",
					},
					cli.BoolFlag{
						Name:  "show-versions,v",
						Usage: "Display versions available to upgrade to",
					},
					cli.StringSliceFlag{
						Name: "target,t",
						Usage: "Target project names/ids to upgrade. Specified targets on upgrade will override all " +
							"the original targets. Leave it empty to keep current targets",
					},
				},
			},
			cli.Command{
				Name:        "list-templates",
				Aliases:     []string{"lt"},
				Usage:       "List templates available for installation",
				Description: "\nList all app templates in the current Rancher server",
				ArgsUsage:   "None",
				Action:      globalTemplateLs,
				Flags: []cli.Flag{
					formatFlag,
					cli.StringFlag{
						Name:  "catalog",
						Usage: "Specify the catalog to list templates for",
					},
				},
			},
			cli.Command{
				Name:        "show-template",
				Aliases:     []string{"st"},
				Usage:       "Show versions available to install for an app template",
				Description: "\nShow all available versions of an app template",
				ArgsUsage:   "[TEMPLATE_ID]",
				Action:      templateShow,
			},
			cli.Command{
				Name:      "show-app",
				Aliases:   []string{"sa"},
				Usage:     "Show an app's available versions and revisions",
				ArgsUsage: "[APP_NAME/APP_ID]",
				Action:    showMultiClusterApp,
				Flags: []cli.Flag{
					formatFlag,
				},
			},
		},
	}
}

func multiClusterAppLs(ctx *cli.Context) error {
	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	collection, err := c.ManagementClient.MultiClusterApp.List(defaultListOpts(ctx))
	writer := NewTableWriter([][]string{
		{"NAME", "App.Name"},
		{"STATE", "App.State"},
		{"VERSION", "Version"},
		{"TARGET_PROJECTS", "Targets"},
	}, ctx)

	defer writer.Close()

	clusterCache, projectCache, err := getClusterProjectMap(ctx, c.ManagementClient)
	if err != nil {
		return err
	}

	templateVersionCache := make(map[string]string)
	for _, item := range collection.Data {
		version, err := getTemplateVersion(c.ManagementClient, templateVersionCache, item.TemplateVersionID)
		if err != nil {
			return err
		}
		targetNames := getReadableTargetNames(clusterCache, projectCache, item.Targets)
		writer.Write(&MultiClusterAppData{
			App:     item,
			Version: version,
			Targets: strings.Join(targetNames, ","),
		})
	}
	return writer.Err()
}

func getTemplateVersion(client *managementClient.Client, templateVersionCache map[string]string, ID string) (string, error) {
	var version string
	if cachedVersion, ok := templateVersionCache[ID]; ok {
		version = cachedVersion
	} else {
		templateVersion, err := client.TemplateVersion.ByID(ID)
		if err != nil {
			return "", err
		}
		templateVersionCache[templateVersion.ID] = templateVersion.Version
		version = templateVersion.Version
	}
	return version, nil
}

func getClusterProjectMap(ctx *cli.Context, client *managementClient.Client) (map[string]managementClient.Cluster, map[string]managementClient.Project, error) {
	clusters := make(map[string]managementClient.Cluster)
	clusterCollectionData, err := listAllClusters(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range clusterCollectionData {
		clusters[c.ID] = c
	}
	projects := make(map[string]managementClient.Project)
	projectCollectionData, err := listAllProjects(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range projectCollectionData {
		projects[p.ID] = p
	}
	return clusters, projects, nil
}

func listAllClusters(ctx *cli.Context, client *managementClient.Client) ([]managementClient.Cluster, error) {
	clusterCollection, err := client.Cluster.List(defaultListOpts(ctx))
	if err != nil {
		return nil, err
	}
	clusterCollectionData := clusterCollection.Data
	for {
		clusterCollection, err = clusterCollection.Next()
		if err != nil {
			return nil, err
		}
		if clusterCollection == nil {
			break
		}
		clusterCollectionData = append(clusterCollectionData, clusterCollection.Data...)
		if !clusterCollection.Pagination.Partial {
			break
		}
	}
	return clusterCollectionData, nil
}

func listAllProjects(ctx *cli.Context, client *managementClient.Client) ([]managementClient.Project, error) {
	projectCollection, err := client.Project.List(defaultListOpts(ctx))
	if err != nil {
		return nil, err
	}
	projectCollectionData := projectCollection.Data
	for {
		projectCollection, err = projectCollection.Next()
		if err != nil {
			return nil, err
		}
		if projectCollection == nil {
			break
		}
		projectCollectionData = append(projectCollectionData, projectCollection.Data...)
		if !projectCollection.Pagination.Partial {
			break
		}
	}
	return projectCollectionData, nil
}

func getReadableTargetNames(clusterCache map[string]managementClient.Cluster, projectCache map[string]managementClient.Project, targets []managementClient.Target) []string {
	var targetNames []string
	for _, target := range targets {
		projectID := target.ProjectID
		clusterID, _ := parseScope(projectID)
		cluster, ok := clusterCache[clusterID]
		if !ok {
			logrus.Debugf("Cannot get readable name for target %q, showing ID", target.ProjectID)
			targetNames = append(targetNames, target.ProjectID)
			continue
		}
		project, ok := projectCache[projectID]
		if !ok {
			logrus.Debugf("Cannot get readable name for target %q, showing ID", target.ProjectID)
			targetNames = append(targetNames, target.ProjectID)
			continue
		}
		targetNames = append(targetNames, concatScope(cluster.Name, project.Name))
	}
	return targetNames
}

func multiClusterAppDelete(ctx *cli.Context) error {
	if ctx.NArg() == 0 {
		return cli.ShowSubcommandHelp(ctx)
	}

	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	for _, arg := range ctx.Args() {
		resource, err := Lookup(c, arg, managementClient.MultiClusterAppType)
		if err != nil {
			return err
		}

		app, err := c.ManagementClient.MultiClusterApp.ByID(resource.ID)
		if err != nil {
			return err
		}

		err = c.ManagementClient.MultiClusterApp.Delete(app)
		if err != nil {
			return err
		}
	}

	return nil
}

func multiClusterAppUpgrade(ctx *cli.Context) error {
	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	if ctx.Bool("show-versions") {
		return outputMultiClusterAppVersions(ctx, c)
	}

	if ctx.NArg() < 2 {
		return cli.ShowSubcommandHelp(ctx)
	}

	resource, err := Lookup(c, ctx.Args().First(), managementClient.MultiClusterAppType)
	if err != nil {
		return err
	}

	app, err := c.ManagementClient.MultiClusterApp.ByID(resource.ID)
	if err != nil {
		return err
	}

	answers := fromMultiClusterAppAnswers(app.Answers)
	err = processAnswers(ctx, c, nil, answers, false)
	if err != nil {
		return err
	}
	app.Answers, err = toMultiClusterAppAnswers(c, answers)
	if err != nil {
		return err
	}

	version := ctx.Args().Get(1)
	templateVersion, err := c.ManagementClient.TemplateVersion.ByID(app.TemplateVersionID)
	if err != nil {
		return err
	}
	app.TemplateVersionID = strings.TrimSuffix(templateVersion.ID, templateVersion.Version) + version

	projectIDs, err := lookupProjectIDsFromTargets(c, ctx.StringSlice("target"))
	if err != nil {
		return err
	}
	if len(projectIDs) > 0 {
		app.Targets = nil
		for _, target := range projectIDs {
			app.Targets = append(app.Targets, managementClient.Target{
				ProjectID: target,
			})
		}
	}

	_, err = c.ManagementClient.MultiClusterApp.Update(app, app)
	return err
}

func multiClusterAppRollback(ctx *cli.Context) error {
	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	if ctx.Bool("show-revisions") {
		return outputMultiClusterAppRevisions(ctx, c)
	}

	if ctx.NArg() < 2 {
		return cli.ShowSubcommandHelp(ctx)
	}

	resource, err := Lookup(c, ctx.Args().First(), managementClient.MultiClusterAppType)
	if err != nil {
		return err
	}

	app, err := c.ManagementClient.MultiClusterApp.ByID(resource.ID)
	if err != nil {
		return err
	}

	revisionResource, err := Lookup(c, ctx.Args().Get(1), managementClient.MultiClusterAppRevisionType)
	if err != nil {
		return err
	}

	rr := &managementClient.MultiClusterAppRollbackInput{
		RevisionID: revisionResource.ID,
	}
	err = c.ManagementClient.MultiClusterApp.ActionRollback(app, rr)
	return err
}

func multiClusterAppTemplateInstall(ctx *cli.Context) error {
	if ctx.NArg() == 0 {
		return cli.ShowSubcommandHelp(ctx)
	}
	templateName := ctx.Args().First()
	appName := ctx.Args().Get(1)

	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	app := &managementClient.MultiClusterApp{
		Name: appName,
	}

	resource, err := Lookup(c, templateName, managementClient.TemplateType)
	if err != nil {
		return err
	}
	template, err := c.ManagementClient.Template.ByID(resource.ID)
	if err != nil {
		return err
	}

	templateVersionID := templateVersionIDFromVersionLink(template.VersionLinks[template.DefaultVersion])
	userVersion := ctx.String("version")
	if userVersion != "" {
		if link, ok := template.VersionLinks[userVersion]; ok {
			templateVersionID = templateVersionIDFromVersionLink(link)
		} else {
			return fmt.Errorf(
				"version %s for template %s is invalid, run 'rancher mcapp show-template %s' for a list of versions",
				userVersion,
				templateName,
				templateName,
			)
		}
	}

	templateVersion, err := c.ManagementClient.TemplateVersion.ByID(templateVersionID)
	if err != nil {
		return err
	}

	interactive := !ctx.Bool("no-prompt")
	answers := make(map[string]string)
	err = processAnswers(ctx, c, templateVersion, answers, interactive)
	if err != nil {
		return err
	}

	projectIDs, err := lookupProjectIDsFromTargets(c, ctx.StringSlice("target"))
	if err != nil {
		return err
	}

	for _, target := range projectIDs {
		app.Targets = append(app.Targets, managementClient.Target{
			ProjectID: target,
		})
	}
	if len(projectIDs) == 0 {
		app.Targets = append(app.Targets, managementClient.Target{
			ProjectID: c.UserConfig.Project,
		})
	}

	app.Answers, err = toMultiClusterAppAnswers(c, answers)
	if err != nil {
		return err
	}
	app.TemplateVersionID = templateVersionID

	madeApp, err := c.ManagementClient.MultiClusterApp.Create(app)
	if err != nil {
		return err
	}

	var (
		timewait  int
		installed bool
	)
	timeout := ctx.Int("timeout")
	for !installed {
		if timewait*2 >= timeout {
			return errors.New("timed out waiting for app to be active, the app could still be installing. Run 'rancher multiclusterapps' to verify")
		}
		timewait++
		time.Sleep(2 * time.Second)
		madeApp, err = c.ManagementClient.MultiClusterApp.ByID(madeApp.ID)
		if err != nil {
			return err
		}
		for _, condition := range madeApp.Status.Conditions {
			condType := strings.ToLower(condition.Type)
			condStatus := strings.ToLower(condition.Status)
			if condType == "installed" && condStatus == "true" {
				installed = true
				break
			}
		}
		if madeApp.Transitioning == "error" {
			return errors.New(madeApp.TransitioningMessage)
		}
	}
	return nil
}

func lookupProjectIDsFromTargets(c *cliclient.MasterClient, targets []string) ([]string, error) {
	var projectIDs []string
	for _, target := range targets {
		projectID, err := lookupProjectIDFromProjectScope(c, target)
		if err != nil {
			return nil, err
		}
		projectIDs = append(projectIDs, projectID)
	}
	return projectIDs, nil
}

func lookupClusterIDFromClusterScope(c *cliclient.MasterClient, clusterNameOrID string) (string, error) {
	clusterResource, err := Lookup(c, clusterNameOrID, managementClient.ClusterType)
	if err != nil {
		return "", err
	}
	return clusterResource.ID, nil
}

func lookupProjectIDFromProjectScope(c *cliclient.MasterClient, scope string) (string, error) {
	cluster, project := parseScope(scope)
	clusterResource, err := Lookup(c, cluster, managementClient.ClusterType)
	if err != nil {
		return "", err
	}
	if clusterResource.ID == cluster {
		// Lookup by ID
		projectResource, err := Lookup(c, scope, managementClient.ProjectType)
		if err != nil {
			return "", err
		}
		return projectResource.ID, nil
	}
	// Lookup by clusterName:projectName
	projectResource, err := Lookup(c, project, managementClient.ProjectType)
	if err != nil {
		return "", err
	}
	return projectResource.ID, nil

}

func toMultiClusterAppAnswers(c *cliclient.MasterClient, answers map[string]string) ([]managementClient.Answer, error) {
	answerMap := make(map[string]map[string]string)
	var answerArray []managementClient.Answer
	for k, v := range answers {
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 1 {
			//global scope
			if answerMap[""] == nil {
				answerMap[""] = make(map[string]string)
			}
			answerMap[""][k] = v
		} else if len(parts) == 2 {
			//cluster scope
			clusterNameOrID := parts[0]
			clusterID, err := lookupClusterIDFromClusterScope(c, clusterNameOrID)
			if err != nil {
				return nil, err
			}
			setValueInAnswerMap(answerMap, clusterNameOrID, clusterID, parts[1], v)
		} else if len(parts) == 3 {
			//project scope
			projectScope := concatScope(parts[0], parts[1])
			projectID, err := lookupProjectIDFromProjectScope(c, projectScope)
			if err != nil {
				return nil, err
			}
			setValueInAnswerMap(answerMap, projectScope, projectID, parts[2], v)
		}
	}
	for k, v := range answerMap {
		answer := managementClient.Answer{
			Values: v,
		}
		if strings.Contains(k, ":") {
			answer.ProjectID = k
		} else if k != "" {
			answer.ClusterID = k
		}
		answerArray = append(answerArray, answer)
	}
	return answerArray, nil
}

func setValueInAnswerMap(answerMap map[string]map[string]string, scope string, scopeID string, plainKey string, value string) {
	if answerMap[scopeID] == nil {
		answerMap[scopeID] = make(map[string]string)
	}
	if _, ok := answerMap[scopeID][plainKey]; ok {
		// It is possible that there are different forms of the same answer key in aggregated answers
		// In this case, name format from users overrides id format from existing app answers.
		if scope != scopeID {
			answerMap[scopeID][plainKey] = value
		}
	} else {
		answerMap[scopeID][plainKey] = value
	}
}

func fromMultiClusterAppAnswers(answers []managementClient.Answer) map[string]string {
	answerMap := make(map[string]string)
	for _, answer := range answers {
		for k, v := range answer.Values {
			scope := ""
			if answer.ProjectID != "" {
				scope = answer.ProjectID
			} else if answer.ClusterID != "" {
				scope = answer.ClusterID
			}

			scopedKey := k
			if scope != "" {
				scopedKey = concatScope(scope, k)
			}
			answerMap[scopedKey] = v
		}
	}
	return answerMap
}

func showMultiClusterApp(ctx *cli.Context) error {
	if ctx.NArg() == 0 {
		return cli.ShowSubcommandHelp(ctx)
	}

	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	err = outputMultiClusterAppRevisions(ctx, c)
	if err != nil {
		return err
	}

	fmt.Println()

	err = outputMultiClusterAppVersions(ctx, c)
	if err != nil {
		return err
	}
	return nil
}

func outputMultiClusterAppVersions(ctx *cli.Context, c *cliclient.MasterClient) error {
	if ctx.NArg() == 0 {
		return cli.ShowSubcommandHelp(ctx)
	}

	resource, err := Lookup(c, ctx.Args().First(), managementClient.MultiClusterAppType)
	if err != nil {
		return err
	}

	app, err := c.ManagementClient.MultiClusterApp.ByID(resource.ID)
	if err != nil {
		return err
	}

	templateVersion, err := c.ManagementClient.TemplateVersion.ByID(app.TemplateVersionID)
	if err != nil {
		return err
	}

	template := &managementClient.Template{}
	if err := c.ManagementClient.Ops.DoGet(templateVersion.Links["template"], &types.ListOpts{}, template); err != nil {
		return err
	}
	writer := NewTableWriter([][]string{
		{"CURRENT", "Current"},
		{"VERSION", "Version"},
	}, ctx)

	defer writer.Close()

	sortedVersions, err := sortTemplateVersions(template)
	if err != nil {
		return err
	}

	for _, version := range sortedVersions {
		var current string
		if version.String() == templateVersion.Version {
			current = "*"
		}
		writer.Write(&VersionData{
			Current: current,
			Version: version.String(),
		})
	}
	return writer.Err()
}

func outputMultiClusterAppRevisions(ctx *cli.Context, c *cliclient.MasterClient) error {
	if ctx.NArg() == 0 {
		return cli.ShowSubcommandHelp(ctx)
	}

	resource, err := Lookup(c, ctx.Args().First(), managementClient.MultiClusterAppType)
	if err != nil {
		return err
	}

	app, err := c.ManagementClient.MultiClusterApp.ByID(resource.ID)
	if err != nil {
		return err
	}

	revisions := &managementClient.MultiClusterAppRevisionCollection{}
	err = c.ManagementClient.GetLink(*resource, "revisions", revisions)
	if err != nil {
		return err
	}

	var sorted revSlice
	for _, rev := range revisions.Data {
		parsedTime, err := time.Parse(time.RFC3339, rev.Created)
		if nil != err {
			return err
		}
		sorted = append(sorted, revision{Name: rev.Name, Created: parsedTime})
	}

	sort.Sort(sorted)

	writer := NewTableWriter([][]string{
		{"CURRENT", "Current"},
		{"REVISION", "Name"},
		{"CREATED", "Human"},
	}, ctx)

	defer writer.Close()

	for _, rev := range sorted {
		if rev.Name == app.Status.RevisionID {
			rev.Current = "*"
		}
		rev.Human = rev.Created.Format("02 Jan 2006 15:04:05 MST")
		writer.Write(rev)

	}
	return writer.Err()
}

func globalTemplateLs(ctx *cli.Context) error {
	c, err := GetClient(ctx)
	if err != nil {
		return err
	}

	filter := defaultListOpts(ctx)
	if ctx.String("catalog") != "" {
		resource, err := Lookup(c, ctx.String("catalog"), managementClient.CatalogType)
		if err != nil {
			return err
		}
		filter.Filters["catalogId"] = resource.ID
	}

	collection, err := c.ManagementClient.Template.List(filter)
	if err != nil {
		return err
	}

	writer := NewTableWriter([][]string{
		{"ID", "ID"},
		{"NAME", "Template.Name"},
		{"CATEGORY", "Category"},
	}, ctx)

	defer writer.Close()

	for _, item := range collection.Data {
		// Skip non-global catalogs
		if item.CatalogID == "" {
			continue
		}
		writer.Write(&TemplateData{
			ID:       item.ID,
			Template: item,
			Category: strings.Join(item.Categories, ","),
		})
	}

	return writer.Err()
}

func concatScope(scope, key string) string {
	return fmt.Sprintf("%s:%s", scope, key)
}

func parseScope(ref string) (scope string, key string) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}