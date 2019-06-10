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
	"os"
	"io/ioutil"
	"log"
	"sync"
	"strings"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/admin/directory/v1"
	"cloud.google.com/go/storage"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/cloudresourcemanager/v1"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/iterator"

)

var (
	wg sync.WaitGroup
	wg2 sync.WaitGroup
	serviceAccountFile = "svc_account.json"
	subject = "admin@esodemoapp2.com"
	cx = "C023zw3x8"


  adminService *admin.Service
	iamService *iam.Service
	crmService *cloudresourcemanager.Service

	projects = make([]*cloudresourcemanager.Project, 0)

	projectsConfig = "projects.groovy"
	pmutex = &sync.Mutex{}
	pfile  *os.File

	usersConfig = "users.groovy"
	umutex = &sync.Mutex{}
	ufile  *os.File

	iamConfig = "iam.groovy"
	imutex = &sync.Mutex{}
	ifile  *os.File

	serviceAccountConfig = "serviceaccounts.groovy"
	smutex = &sync.Mutex{}
	sfile  *os.File
	
	rolesConfig = "roles.groovy"
	rmutex = &sync.Mutex{}
	rfile  *os.File

	groupsConfig = "groups.groovy"
	gmutex = &sync.Mutex{}
	gfile  *os.File

	gcsConfig = "gcs.groovy"
	gcsmutex = &sync.Mutex{}
	gcsfile  *os.File	
)

const (
)

func applyGroovy(cmd string, srcFile string){

	switch srcFile {
	case projectsConfig:
		pmutex.Lock()
		_, err := pfile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		pmutex.Unlock()
	case usersConfig:
		umutex.Lock()
		_, err := ufile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		umutex.Unlock()
	case iamConfig:
		imutex.Lock()
		_, err := ifile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		imutex.Unlock()
	case serviceAccountConfig:
		smutex.Lock()
		_, err := sfile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		smutex.Unlock()
	case rolesConfig:
		rmutex.Lock()
		_, err := rfile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		rmutex.Unlock()
	case groupsConfig:
		gmutex.Lock()
		_, err := gfile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		gmutex.Unlock()
	case gcsConfig:
		gcsmutex.Lock()
		_, err := gcsfile.WriteString(cmd)
		if err != nil {
			log.Fatal(err)
		}
		gcsmutex.Unlock()									
	}

	//fmt.Println(cmd)
}

func getUsers(ctx context.Context) {
	defer wg.Done()

	pageToken := ""
	for {
	  q :=adminService.Users.List().Customer(cx)
	  if pageToken != "" {
			q = q.PageToken(pageToken)
	  }
	  r, err := q.Do()
	  if err != nil {
			log.Fatal(err)
	  }
	  for _, u := range r.Users {

			entry := `	
			if (g.V().hasLabel('user').has('email','%s').hasNext() == false) {	  
			  g.addV('user').property(label, 'user').property('email', '%s').property('isExternal', false).id().next()
			}
			`		  
		    entry = fmt.Sprintf(entry, u.PrimaryEmail,u.PrimaryEmail)
		    applyGroovy(entry, usersConfig)				
	  }
	  pageToken = r.NextPageToken
	  if pageToken == "" {
			break
	  }
	}
}

func getGroups(ctx context.Context) {
	defer wg.Done()	

	pageToken := ""
	for {
	  q :=adminService.Groups.List().Customer(cx)
	  if pageToken != "" {
			q = q.PageToken(pageToken)
	  }
	  r, err := q.Do()
	  if err != nil {
			log.Fatal(err)
	  }
	  for _, g := range r.Groups {
			entry := `	
			if (g.V().hasLabel('group').has('email','%s').hasNext() == false) {				  		  
			  g.addV('group').property(label, 'group').property('email', '%s').property('isExternal', false).id().next()
			}
			`		  
		    entry = fmt.Sprintf(entry, g.Email, g.Email)
		    applyGroovy(entry, groupsConfig)

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

	pageToken := ""
	for {
		
		q :=adminService.Members.List(memberKey)
	  if pageToken != "" {
			q = q.PageToken(pageToken)
	  }
	  r, err := q.Do()
	  if err != nil {
			if (err.Error() == "googleapi: Error 403: Not Authorized to access this resource/api, forbidden") {
				// ok, so we've got a group we can't expand on...this means we don't own it...
				// this is important and we should error log this pretty clearly
				log.Printf("Group %s cannot be expanded for members;  Possibly a group outside of the Gsuites domain",memberKey )
				return
			}
			log.Fatal(err)
		}
	  for _, m := range r.Members {
			if (m.Type == "CUSTOMER") {
				entry := `
				if (g.V().hasLabel('group').has('email','%s').hasNext() == false) {
					g1 = g.V().hasLabel('group').has('email', '%s').next()
					e1 = g.V().addE('in').to(g1).property('weight', 1).next()
				}
				`		  
				entry = fmt.Sprintf(entry, memberKey, memberKey)
				applyGroovy(entry, groupsConfig)
			}
			if (m.Type == "USER") {
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
			if (m.Type == "GROUP") {
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

	for _, p := range projects {
		req := iamService.Projects.ServiceAccounts.List("projects/" + p.ProjectId)

		if err := req.Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
			for _, sa := range page.Accounts {
							entry := `
							if (g.V().hasLabel('serviceAccount').has('email','%s').hasNext() == false) {
								g.addV('serviceAccount').property(label, 'serviceAccount').property('email', '%s').id().next()
							}
							`		  
							entry = fmt.Sprintf(entry, sa.Email, sa.Email)
							applyGroovy(entry, serviceAccountConfig)						
			}
			return nil
		}); err != nil {
				log.Fatal(err)
		}	
	}
}

func getGCS(ctx context.Context) {
	defer wg.Done()

	data, err := ioutil.ReadFile(serviceAccountFile)
	if err != nil {
			log.Fatal(err)
	}
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON(data))
	if err != nil {
					log.Fatalf("Failed to create client: %v", err)
	}

	for _, p := range projects {

		wg.Add(1)
		go func(ctx context.Context,  projectId string) {
			defer wg.Done()
			it := client.Buckets(ctx, projectId)

			for {
				b, err := it.Next()
				if err == iterator.Done {
						break
				}
				if err != nil {
						log.Fatalf("Unable to iterate bucket %s", b.Name)
				}	

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
				entry = fmt.Sprintf(entry, b.Name, projectId, b.Name, projectId, b.Name,projectId,projectId,projectId,projectId)

				policy, err := client.Bucket(b.Name).IAM().Policy(ctx)
				if err != nil {
					log.Printf("Unable to iterate bucket policy %s", b.Name)
				} else {
					for _, role := range policy.Roles() {
									//log.Printf("        Role  %q", role)
									roleentry :=  `
									if (g.V().hasLabel('role').has('rolename','%s').has('projectid','%s').hasNext() == false) {
										g.addV('role').property(label, 'role').property('rolename', '%s').property('projectid','%s').id().next()
									}
									r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid','%s').next()
									if ( g.V().hasLabel('bucket').has('bucketname', '%s').hasNext()  == false) {
										g.addV('bucket').property(label, 'bucket').property('bucketname', '%s').property('projectid','%s').id().next()
									}
									p1 = g.V().hasLabel('bucket').has('bucketname', '%s').next()
									if (g.V(r1).outE('in').where(inV().hasId( p1.id() )).hasNext() == false) {						
										e1 = g.V(r1).addE('in').to(p1).property('weight', 1).next()	
									}									
									`
									roleentry = fmt.Sprintf(roleentry, role, projectId, role, projectId, role, projectId, b.Name, b.Name, projectId, b.Name)
									memberentry := ``
									
									for _, member := range policy.Members(role) {

										memberType := strings.Split(member,":")[0]
										email := strings.Split(member,":")[1]

										memberentry = memberentry + `																				
										if (g.V().hasLabel('%s').has('email', '%s').hasNext()  == false) {
											g.addV('%s').property(label, '%s').property('email', '%s').id().next()
										}
										i1 = g.V().hasLabel('%s').has('email', '%s').next()
										r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').next()			
										if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
											e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
										}
										`		
										 
										memberentry =  fmt.Sprintf(memberentry, memberType, email, memberType, memberType, email, memberType, email, role, projectId)

									}
									entry = entry + roleentry + memberentry
									applyGroovy(entry, gcsConfig)
					}
				}
			}
					
		}(ctx, p.ProjectId)
	
	}
}

func getRoles(ctx context.Context) {
	defer wg.Done()

	req := crmService.Projects.List()
	if err := req.Pages(ctx, func(page *cloudresourcemanager.ListProjectsResponse) error {
					for _, project := range page.Projects {

						wg.Add(1)
						go func(ctx context.Context,  projectId string) {
							defer wg.Done()
							req := iamService.Projects.Roles.List("projects/" + projectId)
							if err := req.Pages(ctx, func(page *iam.ListRolesResponse) error {
								for _, r := range page.Roles {						
										entry := `
										if (g.V().hasLabel('role').has('rolename','%s').has('projectid','%s').hasNext() == false) {
											g.addV('role').property(label, 'role').property('rolename', '%s').property('projectid','%s').id().next()
										}
										`		  
										entry = fmt.Sprintf(entry, r.Name, projectId, r.Name, projectId)
										applyGroovy(entry, rolesConfig)
								}
								return nil
							}); err != nil {
									log.Fatal(err)
							}	
						}(ctx, project.ProjectId)

					}
					return nil
	}); err != nil {
					log.Fatal(err)
	}	
}

func getIamPolicy(ctx context.Context, projectID string) {
	defer wg.Done()	
	
	rb := &cloudresourcemanager.GetIamPolicyRequest{}

	resp, err := crmService.Projects.GetIamPolicy(projectID, rb).Context(ctx).Do()
	if err != nil {
		log.Fatal(err)
	}

	for _, b := range resp.Bindings {
		entry := `
		if (g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').hasNext()  == false) {
			g.addV('role').property(label, 'role').property('rolename', '%s').property('projectid', '%s').id().next()
		}
		r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').next()
		if ( g.V().hasLabel('project').has('projectid', '%s').hasNext()  == false) {
			g.addV('project').property(label, 'project').property('projectid', '%s').id().next()
		}
		p1 = g.V().hasLabel('project').has('projectid', '%s').next()
		if (g.V(r1).outE('in').where(inV().hasId( p1.id() )).hasNext() == false) {						
			e1 = g.V(r1).addE('in').to(p1).property('weight', 1).next()	
		}
		`		  
		entry = fmt.Sprintf(entry, b.Role, projectID, b.Role, projectID, b.Role, projectID, projectID, projectID, projectID)
		applyGroovy(entry, iamConfig)
	
		for _, m := range b.Members {
		  memberType := strings.Split(m,":")[0]
		  email := strings.Split(m,":")[1]

		  if (memberType == "user") {

			entry := `
			if (g.V().hasLabel('user').has('email', '%s').hasNext()  == false) {
				g.addV('user').property(label, 'user').property('email', '%s').id().next()
			}
			i1 = g.V().hasLabel('user').has('email', '%s').next()
			r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').next()			
			if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
				e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
			}
			`		  
			entry = fmt.Sprintf(entry, email, email, email, b.Role, projectID)
			applyGroovy(entry, iamConfig)
			}
			
		  if (memberType == "serviceAccount") {

				entry := `
				if (g.V().hasLabel('serviceAccount').has('serviceAccount', '%s').hasNext()  == false) {
					g.addV('serviceAccount').property(label, 'serviceAccount').property('email', '%s').id().next()
				}
				i1 = g.V().hasLabel('serviceAccount').has('email', '%s').next()
				r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').next()			
				if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
					e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
				}
				`		  
				entry = fmt.Sprintf(entry, email, email, email, b.Role, projectID)
				applyGroovy(entry, iamConfig)
				}			
		  
		  if (memberType == "group") {
			entry := `
			if (g.V().hasLabel('group').has('email', '%s').hasNext()  == false) {
				g.addV('group').property(label, 'group').property('email', '%s').id().next()
			}
			i1 = g.V().hasLabel('group').has('email', '%s').next()
			r1 = g.V().hasLabel('role').has('rolename', '%s').has('projectid', '%s').next()			
			if (g.V(i1).outE('in').where(inV().hasId(r1.id())).hasNext()  == false) {
				e1 = g.V(i1).addE('in').to(r1).property('weight', 1).next()
			}
			`		  
			entry = fmt.Sprintf(entry,  email, email, email, b.Role, projectID)
			applyGroovy(entry, iamConfig)			  
		  }		  
		}
    }	
}

func getProjectIAM(ctx context.Context) {

	defer wg.Done()

	for _, p := range projects {
		entry := `
			if (g.V().hasLabel('project').has('projectId', '%s').hasNext() == false) {
				g.addV('project').property(label, 'project').property('projectId', '%s').id().next()
			}
		`		  
		entry = fmt.Sprintf(entry, p.ProjectId, p.ProjectId)
		applyGroovy(entry, projectsConfig)
		wg.Add(1)									
		go getIamPolicy(ctx, p.ProjectId)
	}
}

func getProjects(ctx context.Context) {

	req := crmService.Projects.List()
	if err := req.Pages(ctx, func(page *cloudresourcemanager.ListProjectsResponse) error {
			for _, p := range page.Projects {
			 projects =	append(projects, p)
			}
			return nil
	}); err != nil {
					log.Fatal(err)
	}	
}

func main() {
	ctx := context.Background()

	component := flag.String("component", "all", "component to load: choices, all|projectIAM|users|serviceaccounts|roles|groups")
	flag.Parse()

	data, err := ioutil.ReadFile(serviceAccountFile)
	if err != nil {
			log.Fatal(err)
	}

	adminconf, err := google.JWTConfigFromJSON(data,
		admin.AdminDirectoryUserReadonlyScope,admin.AdminDirectoryGroupReadonlyScope,
	)
	adminconf.Subject = subject

	adminService, err = admin.New(adminconf.Client(ctx))
	if err != nil {
		log.Fatal(err)
	}

	iamconf, err := google.JWTConfigFromJSON(data, iam.CloudPlatformScope)
	if err != nil {
			log.Fatal(err)
	}
	iamclient := iamconf.Client(oauth2.NoContext)

	iamService, err = iam.New(iamclient)
	if err != nil {
			log.Fatal(err)
	}

	crmconf, err := google.JWTConfigFromJSON(data, cloudresourcemanager.CloudPlatformReadOnlyScope)
	if err != nil {
		log.Fatal(err)
	}
	crmclient := crmconf.Client(oauth2.NoContext)

	crmService, err = cloudresourcemanager.New(crmclient)
	if err != nil {
		log.Fatal(err)
	}


	getProjects(ctx)

	switch *component {
	case "projectIAM":
		pfile, _ = os.Create(projectsConfig)
		defer pfile.Close()
		wg.Add(1)
		go getProjectIAM(ctx)	
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
	case "roles":
		rfile, _ = os.Create(rolesConfig)
		defer rfile.Close()
		wg.Add(1)		
		go getRoles(ctx)
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
	

		
		wg.Add(6)
		go getUsers(ctx)
		go getGroups(ctx)
		go getProjectServiceAccounts(ctx)		
		go getRoles(ctx)		
		go getProjectIAM(ctx)
		go getGCS(ctx)
	}
	wg.Wait()

}
