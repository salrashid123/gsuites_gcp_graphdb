// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

/* Sample application to load gsuites users, groups and GCP projects, iam permissions
into a janusgraph database

NOTE: this product is NOT supported by Google and has not been tested at scale.
*/

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var (
	wg     sync.WaitGroup
	wg2    sync.WaitGroup
	cmutex = &sync.Mutex{}

	component          = flag.String("component", "all", "component to load: choices, all|projectIAM|users|serviceaccounts|roles|groups")
	serviceAccountFile = flag.String("serviceAccountFile", "svc_account.json", "Servie Account JSON file with IAM permissions to the org")
	subject            = flag.String("subject", "admin@esodemoapp2.com", "Admin user to for the organization")
	organization       = flag.String("organization", "", "OrganizationID")
	cx                 = flag.String("cx", "", "Customer ID number for the Gsuites domain")
	delay              = flag.Int("delay", 100, "delay in ms for each goroutine")
	includePermissions = flag.Bool("includePermissions", false, "Include Permissions in Graph")

	adminService *admin.Service
	iamService   *iam.Service
	crmService   *cloudresourcemanager.Service

	projects = make([]*cloudresourcemanager.Project, 0)

	permissions = &Permissions{}
	roles       = &Roles{}

	limiter *rate.Limiter
	ors     *iam.RolesService

	projectsConfig = "projects.groovy"
	pmutex         = &sync.Mutex{}
	pfile          *os.File

	usersConfig = "users.groovy"
	umutex      = &sync.Mutex{}
	ufile       *os.File

	iamConfig = "iam.groovy"
	imutex    = &sync.Mutex{}
	ifile     *os.File

	serviceAccountConfig = "serviceaccounts.groovy"
	smutex               = &sync.Mutex{}
	sfile                *os.File

	rolesConfig = "roles.groovy"
	rmutex      = &sync.Mutex{}
	rfile       *os.File

	groupsConfig = "groups.groovy"
	gmutex       = &sync.Mutex{}
	gfile        *os.File

	gcsConfig = "gcs.groovy"
	gcsmutex  = &sync.Mutex{}
	gcsfile   *os.File
)

const (
	maxRequestsPerSecond float64 = 4 // "golang.org/x/time/rate" limiter to throttle operations
	burst                int     = 4
)

type Roles struct {
	Roles []Role `json:"roles"`
}

type Role struct {
	Name                string   `json:"name"`
	Role                iam.Role `json:"role"`
	IncludedPermissions []string `json:"included_permissions"`
}

type Permissions struct {
	Permissions []Permission `json:"permissions"`
}

type Permission struct {
	//Permission iam.Permission // there's no direct way to query a given permission detail!
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

func applyGroovy(cmd string, srcFile string) {

	switch srcFile {
	case projectsConfig:
		pmutex.Lock()
		_, err := pfile.WriteString(cmd)
		err = pfile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		pmutex.Unlock()
	case usersConfig:
		umutex.Lock()
		_, err := ufile.WriteString(cmd)
		err = ufile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		umutex.Unlock()
	case iamConfig:
		imutex.Lock()
		_, err := ifile.WriteString(cmd)
		err = ifile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		imutex.Unlock()
	case serviceAccountConfig:
		smutex.Lock()
		_, err := sfile.WriteString(cmd)
		err = sfile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		smutex.Unlock()
	case rolesConfig:
		rmutex.Lock()
		_, err := rfile.WriteString(cmd)
		err = rfile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		rmutex.Unlock()
	case groupsConfig:
		gmutex.Lock()
		_, err := gfile.WriteString(cmd)
		err = gfile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		gmutex.Unlock()
	case gcsConfig:
		gcsmutex.Lock()
		_, err := gcsfile.WriteString(cmd)
		err = gcsfile.Sync()
		if err != nil {
			glog.Fatal(err)
		}
		gcsmutex.Unlock()
	}

	glog.V(10).Infoln(cmd)

}

func getUsers(ctx context.Context) {
	defer wg.Done()
	glog.V(2).Infoln(">>>>>>>>>>> Getting Users")

	pageToken := ""
	for {
		q := adminService.Users.List().Customer(*cx)
		if pageToken != "" {
			q = q.PageToken(pageToken)
		}
		r, err := q.Do()
		if err != nil {
			glog.Fatal(err)
		}
		for _, u := range r.Users {
			glog.V(4).Infoln("            Adding User: ", u.PrimaryEmail)
			entry := `	
if (g.V().hasLabel('user').has('email','%s').hasNext() == false) {
 g.addV('user').property(label, 'user').property('email', '%s').property('isExternal', false).id().next()
}
`
			entry = fmt.Sprintf(entry, u.PrimaryEmail, u.PrimaryEmail)
			applyGroovy(entry, usersConfig)
		}
		pageToken = r.NextPageToken
		time.Sleep(time.Duration(*delay) * time.Millisecond)
		if pageToken == "" {
			break
		}
	}
}

func getGroups(ctx context.Context) {
	defer wg.Done()
	glog.V(2).Infoln(">>>>>>>>>>> Getting Groups")

	// loop over groups twice first time to get group names
	// then group members (we do this to properly sequence the graphcreation/groovy file)
	pageToken := ""
	for {
		q := adminService.Groups.List().Customer(*cx)
		if pageToken != "" {
			q = q.PageToken(pageToken)
		}
		r, err := q.Do()
		if err != nil {
			glog.Fatal(err)
		}
		for _, g := range r.Groups {
			glog.V(4).Infoln("            Adding Group: ", g.Email)
			entry := `	
if (g.V().hasLabel('group').has('email','%s').hasNext() == false) {	  		  
 g.addV('group').property(label, 'group').property('email', '%s').property('isExternal', false).id().next()
}
`
			entry = fmt.Sprintf(entry, g.Email, g.Email)
			applyGroovy(entry, groupsConfig)

		}
		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	pageToken = ""
	for {
		q := adminService.Groups.List().Customer(*cx)
		if pageToken != "" {
			q = q.PageToken(pageToken)
		}
		r, err := q.Do()
		if err != nil {
			glog.Fatal(err)
		}
		for _, g := range r.Groups {
			time.Sleep(time.Duration(*delay) * time.Millisecond)
			wg2.Add(1)
			go getGroupMembers(ctx, g.Email)
		}
		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	wg2.Wait()
}

func getGroupMembers(ctx context.Context, memberKey string) {
	defer wg2.Done()
	glog.V(2).Infoln(">>>>>>>>>>> Getting GroupMembers for Gropup ", memberKey)

	pageToken := ""
	for {

		q := adminService.Members.List(memberKey)
		if pageToken != "" {
			q = q.PageToken(pageToken)
		}
		r, err := q.Do()
		if err != nil {
			if err.Error() == "googleapi: Error 403: Not Authorized to access this resource/api, forbidden" {
				// ok, so we've got a group we can't expand on...this means we don't own it...
				// this is important and we should error log this pretty clearly
				glog.Infof("Group %s cannot be expanded for members;  Possibly a group outside of the Gsuites domain", memberKey)
				return
			}
			glog.Fatal(err)
		}
		for _, m := range r.Members {
			glog.V(4).Infof("            Adding Member to Group %v : %v", memberKey, m.Email)
			if m.Type == "CUSTOMER" {
				entry := `
if (g.V().hasLabel('group').has('email','%s').hasNext() == false) {
 g1 = g.V().hasLabel('group').has('email', '%s').next()
 e1 = g.V().addE('in').to(g1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, memberKey, memberKey)
				applyGroovy(entry, groupsConfig)
			}
			if m.Type == "USER" {
				entry := `
if (g.V().hasLabel('user').has('email', '%s').hasNext() == false) {
 g.addV('user').property(label, 'user').property('email', '%s').next()			
}

u1 = g.V().hasLabel('user').has('email', '%s' ).next()
g1 = g.V().hasLabel('group').has('email', '%s').next()

if ( g.V(u1).outE('in').where(inV().hasId( g1.id() )).hasNext() == false) {
 e1 = g.V(u1).addE('in').to(g1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, m.Email, m.Email, m.Email, memberKey)
				applyGroovy(entry, groupsConfig)

			}
			if m.Type == "GROUP" {
				wg2.Add(1)

				entry := `
if (g.V().hasLabel('group').has('email', '%s' ).hasNext() == false) {		  		  
 g.V().hasLabel('group').has('email', '%s' ).next()
}

g1 = g.V().hasLabel('group').has('email', '%s' ).next()
g2 = g.V().hasLabel('group').has('email', '%s').next()

if (  g.V(g1).outE('in').where(inV().hasId( g2.id() )).hasNext() == false) {
 e1 = g.V(g1).addE('in').to(g2).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, m.Email, m.Email, m.Email, memberKey)
				applyGroovy(entry, groupsConfig)

				time.Sleep(time.Duration(*delay) * time.Millisecond)
				go getGroupMembers(ctx, m.Email)
			}
		}
		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

}

func getProjectServiceAccounts(ctx context.Context) {
	defer wg.Done()
	glog.V(2).Infoln(">>>>>>>>>>> Getting ProjectServiceAccounts")

	for _, p := range projects {
		req := iamService.Projects.ServiceAccounts.List("projects/" + p.ProjectId)

		if err := req.Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
			for _, sa := range page.Accounts {
				glog.V(4).Infof("            Adding ServiceAccount: %v", sa.Email)
				entry := `
if (g.V().hasLabel('serviceAccount').has('email','%s').hasNext() == false) {
 g.addV('serviceAccount').property(label, 'serviceAccount').property('email', '%s').id().next()
}
`
				entry = fmt.Sprintf(entry, sa.Email, sa.Email)
				applyGroovy(entry, serviceAccountConfig)
				time.Sleep(time.Duration(*delay) * time.Millisecond)
			}
			return nil
		}); err != nil {
			glog.Fatal(err)
		}
	}
}

func getGCS(ctx context.Context) {
	defer wg.Done()
	glog.V(2).Infoln(">>>>>>>>>>> Getting GCS")

	data, err := ioutil.ReadFile(*serviceAccountFile)
	if err != nil {
		glog.Fatal(err)
	}
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON(data))
	if err != nil {
		glog.Fatalf("Failed to create client: %v", err)
	}

	for _, p := range projects {

		wg.Add(1)
		time.Sleep(time.Duration(*delay) * time.Millisecond)
		go func(ctx context.Context, projectId string) {
			defer wg.Done()
			it := client.Buckets(ctx, projectId)

			for {
				b, err := it.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					glog.Fatalf("Unable to iterate bucket %s", b.Name)
				}
				glog.V(4).Infof("            Adding Bucket %v from Project %v", b.Name, projectId)
				entry := `
if (g.V().hasLabel('bucket').has('bucketname','%s').has('projectid','%s').hasNext() == false) {
 g.addV('bucket').property(label, 'bucket').property('bucketname', '%s').property('projectid','%s').id().next()
}
r1 = g.V().hasLabel('bucket').has('bucketname','%s').has('projectid','%s').next()

if ( g.V().hasLabel('project').has('projectid', '%s').hasNext()  == false) {
 g.addV('project').property(label, 'project').property('projectid', '%s').id().next()
}

p1 = g.V().hasLabel('project').has('projectid', '%s').next()

if (g.V(r1).outE('in').where(inV().hasId( p1.id() )).hasNext() == false) {				
 e1 = g.V(r1).addE('in').to(p1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, b.Name, projectId, b.Name, projectId, b.Name, projectId, projectId, projectId, projectId)

				policy, err := client.Bucket(b.Name).IAM().Policy(ctx)
				if err != nil {
					glog.Infof("Unable to iterate bucket policy %s", b.Name)
				} else {
					for _, role := range policy.Roles() {
						//glog.Infof("        Role  %q", role)
						glog.V(4).Infof("            Adding Role %v to Bucket %v", role, b.Name)

						roleentry := `
if (g.V().hasLabel('role').has('name','%s').hasNext() == false) {
 v = graph.addVertex('role')
}

r1 = g.V().hasLabel('role').has('name', '%s').next()

if ( g.V().hasLabel('bucket').has('bucketname', '%s').hasNext()  == false) {
 g.addV('bucket').property(label, 'bucket').property('bucketname', '%s').property('projectid',%s).id().next()
}

p1 = g.V().hasLabel('bucket').has('bucketname', '%s').next()

if (g.V(r1).outE('in').where(inV().hasId( p1.id() )).hasNext() == false) {			
 e1 = g.V(r1).addE('in').to(p1).property('weight', 1).next()
}
`
						roleentry = fmt.Sprintf(roleentry, role, role, role, b.Name, b.Name, projectId, b.Name)
						memberentry := ``

						for _, member := range policy.Members(role) {

							if len(strings.Split(member, ":")) != 2 {
								if member == "allUsers" || member == "allAuthenticatedUsers" {

									glog.V(4).Infof("            Adding %s to Bucket Role %v on Bucket %v", member, role, b.Name)
									memberType := "group"
									email := member
									memberentry = memberentry + `
if (g.V().hasLabel('%s').has('email', '%s').hasNext()  == false) {
 g.addV('%s').property(label, '%s').property('email', '%s').id().next()
}

i1 = g.V().hasLabel('%s').has('email', '%s').next()
r1 = g.V().hasLabel('role').has('name', '%s').next()

if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
 e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
}
`

									memberentry = fmt.Sprintf(memberentry, memberType, email, memberType, memberType, email, memberType, email, role)
									break

								} else {
									glog.Error("            Unknown memberType  %v\n", member)
									break
								}
							}

							memberType := strings.Split(member, ":")[0]
							email := strings.Split(member, ":")[1]
							glog.V(4).Infof("            Adding Member %v to Bucket Role %v on Bucket %v", email, role, b.Name)
							memberentry = memberentry + `																		
if (g.V().hasLabel('%s').has('email', '%s').hasNext()  == false) {
 g.addV('%s').property(label, '%s').property('email', '%s').id().next()
}

i1 = g.V().hasLabel('%s').has('email', '%s').next()
r1 = g.V().hasLabel('role').has('name', '%s').next()

if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
 e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
}
`

							memberentry = fmt.Sprintf(memberentry, memberType, email, memberType, memberType, email, memberType, email, role)

						}
						entry = entry + roleentry + memberentry
						applyGroovy(entry, gcsConfig)
					}
				}
			}

		}(ctx, p.ProjectId)

	}
}

func getIamPolicy(ctx context.Context, projectID string) {
	defer wg.Done()
	glog.V(2).Infof(">>>>>>>>>>> Getting IAMPolicy for Project %v", projectID)
	rb := &cloudresourcemanager.GetIamPolicyRequest{}

	resp, err := crmService.Projects.GetIamPolicy(projectID, rb).Context(ctx).Do()
	if err != nil {
		glog.Fatal(err)
	}
	//	rs := iam.NewRolesService(iamService)
	for _, b := range resp.Bindings {
		glog.V(4).Infof("            Adding Binding %v to from  Project %v", b.Role, projectID)

		entry := `
if (g.V().hasLabel('role').has('name', '%s').hasNext()  == false) {

 v = graph.addVertex('role')
 v.property('name', '%s')

}

r1 = g.V().hasLabel('role').has('name', '%s').next()

if ( g.V().hasLabel('project').has('projectid', '%s').hasNext()  == false) {
 g.addV('project').property(label, 'project').property('projectid', '%s').id().next()
}

p1 = g.V().hasLabel('project').has('projectid', '%s').next()

if (g.V(r1).outE('in').where(inV().hasId( p1.id() )).hasNext() == false) {				
 e1 = g.V(r1).addE('in').to(p1).property('weight', 1).next()
}
`
		entry = fmt.Sprintf(entry, b.Role, b.Role, b.Role, projectID, projectID, projectID)
		applyGroovy(entry, iamConfig)

		for _, m := range b.Members {
			memberType := strings.Split(m, ":")[0]
			email := strings.Split(m, ":")[1]
			glog.V(4).Infof("            Adding Member %v to Role %v on Project %v", email, b.Role, projectID)
			if memberType == "user" {

				entry := `
if (g.V().hasLabel('user').has('email', '%s').hasNext()  == false) {
 g.addV('user').property(label, 'user').property('email', '%s').id().next()
}

i1 = g.V().hasLabel('user').has('email', '%s').next()
r1 = g.V().hasLabel('role').has('name', '%s').next()

if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
 e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, email, email, email, b.Role)
				applyGroovy(entry, iamConfig)
			}

			if memberType == "serviceAccount" {

				entry := `
if (g.V().hasLabel('serviceAccount').has('serviceAccount', '%s').hasNext()  == false) {
 g.addV('serviceAccount').property(label, 'serviceAccount').property('email', '%s').id().next()
}

i1 = g.V().hasLabel('serviceAccount').has('email', '%s').next()
r1 = g.V().hasLabel('role').has('name', '%s').next()

if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
 e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, email, email, email, b.Role)
				applyGroovy(entry, iamConfig)
			}

			if memberType == "group" {
				entry := `
if (g.V().hasLabel('group').has('email', '%s').hasNext()  == false) {
 g.addV('group').property(label, 'group').property('email', '%s').id().next()
}
i1 = g.V().hasLabel('group').has('email', '%s').next()
r1 = g.V().hasLabel('role').has('name', '%s').next()			
if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
 e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
}
`
				entry = fmt.Sprintf(entry, email, email, email, b.Role)
				applyGroovy(entry, iamConfig)
			}
		}
	}
}

func getIAM(ctx context.Context) {

	defer wg.Done()
	// oreq, err := crmService.Organizations.Get(fmt.Sprintf("organizations/%s", *organization)).Do()
	// if err != nil {
	// 	glog.Fatal(err)
	// }
	// glog.V(2).Infof("     Organization Name %s", oreq.Name)
	// *organization = oreq.Name

	parent := fmt.Sprintf(fmt.Sprintf("organizations/%s", *organization))
	err := generateMap(ctx, parent)
	if err != nil {
		glog.Fatal(err)
	}
	for _, p := range projects {
		parent := fmt.Sprintf("projects/%s", p.ProjectId)
		err = generateMap(ctx, parent)
		if err != nil {
			glog.Fatal(err)
		}
	}
	parent = ""
	err = generateMap(ctx, parent)
	if err != nil {
		glog.Fatal(err)
	}

	for _, r := range roles.Roles {
		entry := `
if (g.V().hasLabel('role').has('name', '%s').hasNext()  == false) {		
  g.addV('role').property(label, 'role').property('name', '%s').id().next()
}
`
		entry = fmt.Sprintf(entry, r.Name, r.Name)
		applyGroovy(entry, rolesConfig)
	}
	if *includePermissions {
		for _, p := range permissions.Permissions {

			pp := `
i1 = g.V().hasLabel('permission').has('name', '%s').next()
		`
			pp = fmt.Sprintf(pp, p.Name)
			rentry := ""

			for _, r := range p.Roles {
				rentry = rentry + `
if (g.V().hasLabel('role').has('name', '%s').hasNext()  == false) {
  g.addV('role').property(label, 'role').property('name', '%s').id().next()
}
r1 = g.V().hasLabel('role').has('name', '%s').next()
e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
`
				rentry = fmt.Sprintf(rentry, r, r, r)
			}

			entry := `
if (g.V().hasLabel('permission').has('permission', '%s').hasNext()  == false) {
  g.addV('permission').property(label, 'permission').property('name', '%s').id().next()
}

%s

%s
`
			entry = fmt.Sprintf(entry, p.Name, p.Name, pp, rentry)
			applyGroovy(entry, rolesConfig)
		}
	}
	// glog.V(2).Infof("Getting Default Roles/Permissions")
	// parent = ""
	// err = generateMap(ctx, parent)
	// if err != nil {
	// 	glog.Fatal(err)
	// }

	glog.V(2).Infof(">>>>>>>>>>> Getting ProjectIAM")
	for _, p := range projects {
		entry := `
if (g.V().hasLabel('project').has('projectId', '%s').hasNext() == false) {
  g.addV('project').property(label, 'project').property('projectId', '%s').id().next()
}
`
		entry = fmt.Sprintf(entry, p.ProjectId, p.ProjectId)
		applyGroovy(entry, projectsConfig)
		// only active projects appear to allow retrieval of IAM policies
		if p.LifecycleState == "ACTIVE" {
			time.Sleep(time.Duration(*delay) * time.Millisecond)
			wg.Add(1)
			go getIamPolicy(ctx, p.ProjectId)
		}
	}
}

// TODO: only get projects in the selected organization
//       the following get allprojects the service account has access to...
func getProjects(ctx context.Context) {
	glog.V(2).Infof(">>>>>>>>>>> Getting Projects")
	req := crmService.Projects.List()
	if err := req.Pages(ctx, func(page *cloudresourcemanager.ListProjectsResponse) error {
		for _, p := range page.Projects {
			if p.LifecycleState == "ACTIVE" {
				projects = append(projects, p)
			}
		}
		return nil
	}); err != nil {
		glog.Fatal(err)
	}
}

func main() {
	ctx := context.Background()
	flag.Parse()
	limiter = rate.NewLimiter(rate.Limit(maxRequestsPerSecond), burst)
	if *organization == "" || *cx == "" {
		glog.Fatal("--organization and --cx must be specified")
	}

	data, err := ioutil.ReadFile(*serviceAccountFile)
	if err != nil {
		glog.Fatal(err)
	}

	adminconf, err := google.JWTConfigFromJSON(data,
		admin.AdminDirectoryUserReadonlyScope, admin.AdminDirectoryGroupReadonlyScope,
	)
	adminconf.Subject = *subject

	adminService, err = admin.New(adminconf.Client(ctx))
	if err != nil {
		glog.Fatal(err)
	}

	iamconf, err := google.JWTConfigFromJSON(data, iam.CloudPlatformScope)
	if err != nil {
		glog.Fatal(err)
	}
	iamclient := iamconf.Client(oauth2.NoContext)

	iamService, err = iam.New(iamclient)
	if err != nil {
		glog.Fatal(err)
	}
	ors = iam.NewRolesService(iamService)

	crmconf, err := google.JWTConfigFromJSON(data, cloudresourcemanager.CloudPlatformReadOnlyScope)
	if err != nil {
		glog.Fatal(err)
	}
	crmclient := crmconf.Client(oauth2.NoContext)

	crmService, err = cloudresourcemanager.New(crmclient)
	if err != nil {
		glog.Fatal(err)
	}

	getProjects(ctx)

	switch *component {
	case "IAM":
		pfile, _ = os.Create(projectsConfig)
		ifile, _ = os.Create(iamConfig)
		rfile, _ = os.Create(rolesConfig)
		defer pfile.Close()
		defer ifile.Close()
		defer rfile.Close()
		wg.Add(1)
		go getIAM(ctx)
	case "users":
		ufile, _ = os.Create(usersConfig)
		defer ufile.Close()
		wg.Add(1)
		go getUsers(ctx)
	case "groups":
		gfile, _ = os.Create(groupsConfig)
		defer gfile.Close()
		wg.Add(1)
		go getGroups(ctx)
	case "serviceaccounts":
		sfile, _ = os.Create(serviceAccountConfig)
		defer sfile.Close()
		go getProjectServiceAccounts(ctx)
	case "gcs":
		gcsfile, _ = os.Create(gcsConfig)
		defer gcsfile.Close()
		wg.Add(1)
		go getGCS(ctx)

	default:

		pfile, _ = os.Create(projectsConfig)
		ufile, _ = os.Create(usersConfig)
		sfile, _ = os.Create(serviceAccountConfig)
		ifile, _ = os.Create(iamConfig)
		rfile, _ = os.Create(rolesConfig)
		gfile, _ = os.Create(groupsConfig)
		gcsfile, _ = os.Create(gcsConfig)

		defer pfile.Close()
		defer ufile.Close()
		defer sfile.Close()
		defer ifile.Close()
		defer rfile.Close()
		defer gfile.Close()
		defer gcsfile.Close()

		wg.Add(5)
		go getUsers(ctx)
		go getGroups(ctx)
		go getProjectServiceAccounts(ctx)
		go getIAM(ctx)
		go getGCS(ctx)
	}
	wg.Wait()

}

func generateMap(ctx context.Context, parent string) error {
	var wg sync.WaitGroup

	oireq := ors.List().Parent(parent)
	if err := oireq.Pages(ctx, func(page *iam.ListRolesResponse) error {
		for _, sa := range page.Roles {
			wg.Add(1)
			go func(ctx context.Context, wg *sync.WaitGroup, sa *iam.Role) {
				glog.V(20).Infof("%s\n", sa.Name)
				defer wg.Done()
				var err error
				if err := limiter.Wait(ctx); err != nil {
					glog.Fatal(err)
				}
				if ctx.Err() != nil {
					glog.Fatal(err)
				}
				rc, err := ors.Get(sa.Name).Do()
				if err != nil {
					glog.Fatal(err)
				}
				cr := &Role{
					Name:                sa.Name,
					Role:                *sa,
					IncludedPermissions: rc.IncludedPermissions,
				}
				cmutex.Lock()
				_, ok := findRoles(roles.Roles, sa.Name)
				if !ok {
					glog.V(2).Infof("     Iterating Role  %s", sa.Name)
					roles.Roles = append(roles.Roles, *cr)
				}
				cmutex.Unlock()

				for _, perm := range rc.IncludedPermissions {
					glog.V(2).Infof("     Appending Permission %s to Role %s", perm, sa.Name)
					i, ok := findPermission(permissions.Permissions, perm)

					if !ok {
						pmutex.Lock()
						permissions.Permissions = append(permissions.Permissions, Permission{
							Name:  perm,
							Roles: []string{sa.Name},
						})
						pmutex.Unlock()
					} else {
						pmutex.Lock()
						p := permissions.Permissions[i]
						_, ok := find(p.Roles, sa.Name)
						if !ok {
							p.Roles = append(p.Roles, sa.Name)
							permissions.Permissions[i] = p
						}
						pmutex.Unlock()
					}

				}
			}(ctx, &wg, sa)

		}
		return nil
	}); err != nil {
		return err
	}

	wg.Wait()

	return nil
}

func find(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}

func findRoles(slice []Role, val string) (int, bool) {
	for i, item := range slice {
		if item.Name == val {
			return i, true
		}
	}
	return -1, false
}

func findPermission(slice []Permission, val string) (int, bool) {
	for i, item := range slice {
		if item.Name == val {
			return i, true
		}
	}
	return -1, false
}
