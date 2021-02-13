# Representing Gsuites and Google Cloud Org structure as a Graph Database

Sample procedure to import

1. Gsuites Users-Groups
2. Google Cloud IAM Policies (users,Roles,Resource)
3. Google Cloud Projects

into a JanusGraph Database.  

This allows sysadmins to easily 'see' how users, groups are structured across cloud org domain and can surface access privileges not
readily visible (i.e, group of groups, external service accounts).  Visualizing and having nested group structure also allows to easy see
relationships between projects/users/roles/serviceAccounts.

For example, the following trivial section shows how a user has indirect access to a resource via nested groups:


Annotated flow that shows the links

user vertex `user1@`

1. has edge `in` to group vertex `subgroup1@`
   (i.e., user is member of a group)

2. group vertex `subgroup1@` has edge `in` to group vertex `group_of_groups1@`
   (i.e. group of groups)

3. group vertex `group_of_groups1@`  has edge `in` to role vertex `roles/appengine.codeViewer`
   (i.e, this role includes a group)  

4. Role vertex `roles/appengine.codeViewer`  has edge `in` to resource vertex  `gcp-project-200601`
   (i.,e this resource/project has a role assigned to it)

5. Adding IAM Permissions to Role Vertices
   
   It would be useful to add the IAM permissions as multi-valued properties to each "Role" node
   However, as of `2/5/21`, there are some issues i've come across in doing this that i've detailed at the end of the doc.
   Instead, as of `2/13/21`, i've just added the permission<->role maps in directly.  This takes a LONG time to generate


- ![images/cytoscape_annotation.png](images/cytoscape_annotation.png)

You are free to alter the `vertex->edge->vertex` relationship in anyway you want; i just picked this simple scheme for starters.

SysAdmins can optionally query directly via `gremlin` command line for the same information.


> **WARNING:**  this is just a simple proof of concept: the script attached runs serially and the object hierarchy described below is very basic and likely incorrect!

The intent here is to demonstrate how to load some sample data covering cloud org and gsuites structure into a  graphDB

## Schema

The schema used in this sample is pretty basic and indexes a few properties across users, groups, serviceAccounts project, IAM roles, and resources.  However, there is boilerplate snippet that demonstrates iterating other resource types.   For reference, see `func getGCS(ctx context.Context)`

The code snippet contained there iterates projects and for each bucket in that project, extracts the IAM bindings and members.  The idea is to use that info to generate groovy snippets to emit to a file.

-![image/IAM_UML.png](images/IAM_UML.png)

> Note: the script and sample below does not cover cloud org, pubsub or GCE resource types.  It only iterates and covers projects

- Users
```python
  g.addV('user').property(label, 'user').property('email', email).id().next()
```

- ServiceAccount
```python
  g.addV('serviceAccount').property(label, 'serviceAccount').property('email', email).id().next()
```

- Groups
```python
  g.addV('group').property(label, 'group').property('email', group_email).next()
```

- Projects
```python
  g.addV('project').property(label, 'project').property('projectId', projectId).id().next()
```

- Roles
```python
  g.addV('role').property(label, 'role').property('name', name).id().next()  
```


## Setup

The setup steps for this script primarily involves configuring a service account for both domain-wide-delegation and GCP cloud org access.


### Configure Service Account for Domain Wide Delegation

1a. Create Service Account in Google Cloud Console (IAM & Admin > Service Accounts)

1b. Edit Service Account and Check "Enable G Suite Domain-wide Delegation"

![images/admin_dwd.png](images/admin_dwd.png)


1c) Find Oauth2 Client ID:

 ```109909984808619572478```

1d) Create API Client in Google Workspace Admin Console (Security > API Controls > Domain-wide Delegation) 
![images/admin_api_create.png](images/admin_api_create.png)

Specify the following OAuth scopes which gives access to view users and groups in your domain respectively:
* `https://www.googleapis.com/auth/admin.directory.user.readonly`
* `https://www.googleapis.com/auth/admin.directory.group.readonly`

For a list of scopes, see [Admin Directory API](`https://developers.google.com/identity/protocols/oauth2/scopes#admin-directory`)

Confirm Client ID scopes by clicking 'View details':

- ![images/admin_api_scope.png](images/admin_api_scope.png)

### Identify Gsuites CustomerID

You can derive it from your gsuites admin console [here](https://stackoverflow.com/questions/33493998/how-do-i-find-the-immutable-id-of-my-google-apps-account?answertab=votes#tab-top).


In my case its:

```
"customerId": "C023zw3x8"
```

alternatively,

```
$ gcloud organizations list
DISPLAY_NAME               ID  DIRECTORY_CUSTOMER_ID
esodemoapp2.com  673208786098              C023zw3x8

```

### Configure Service Account for Cloud Org Access


Set cloud ORG policies to apply from the root org node to all resources in the tree:

1e) Provide Service Account Org-wide Access in Cloud Console (IAM & Admin > IAM)

* Security Reviewer
* Editor


![images/iam_role.png](images/iam_role.png)


1f) Generate and download a the service_account key

   - Make sure the project where you are generating the key has the following APIs enabled:

*  [directory_v1](https://godoc.org/google.golang.org/api/admin/directory/v1)
*  [iam](https://godoc.org/google.golang.org/api/iam/v1)
*  [cloudresourcemanager](https://godoc.org/google.golang.org/api/cloudresourcemanager/v1beta1)
*  [Admin SDK](https://console.developers.google.com/apis/api/admin.googleapis.com/overview)]

## Install JanusGraph

- Local
Download and untar [JanusGraph](http://janusgraph.org/).  I used version [janusgraph-0.3.0-hadoop2](https://github.com/JanusGraph/janusgraph/releases/tag/v0.3.0)

* Start JanusGraph with defaults (Cassandra, ElasticSearch local)  
  Note, you need [java8](https://docs.janusgraph.org/getting-started/installation/#local-installation)

```bash
export JAVA_HOME=/path/to/jre1.8.0_211/

$ janusgraph-0.3.0-hadoop2/bin/janusgraph.sh start
Forking Cassandra...
Running `nodetool statusthrift`.. OK (returned exit status 0 and printed string "running").
Forking Elasticsearch...
Connecting to Elasticsearch (127.0.0.1:9200)..... OK (connected to 127.0.0.1:9200).
Forking Gremlin-Server...
Connecting to Gremlin-Server (127.0.0.1:8182)..... OK (connected to 127.0.0.1:8182).
Run gremlin.sh to connect.
```

* Connect via Gremlin

- [gremlin-server](http://tinkerpop.apache.org/docs/current/reference/#gremlin-server)

```bash
$ janusgraph-0.3.0-hadoop2/bin/gremlin.sh

         \,,,/
         (o o)
-----oOOo-(3)-oOOo-----
SLF4J: Class path contains multiple SLF4J bindings.
plugin activated: janusgraph.imports
plugin activated: tinkerpop.server
plugin activated: tinkerpop.gephi
plugin activated: tinkerpop.utilities
gremlin>
```

- Setup Gremlin local connection

```
:remote connect tinkerpop.server conf/remote.yaml session
:remote console
```

At this point, local scripts on local will get sent to the running gremlin server

## Configure and Run ETL script

- `serviceAccountFile`: path to the service account file
- `subject`:  the email/identity of a gsuites domain admin to represent
- `cx`: Gsuites customerID

then on a system with `go 1.11`, run

```
go run main.go \
  --serviceAccountFile=/path/to/svc_account.json \
  --subject=admin@esodemoapp2.com \
  --component=all \
  --cx=C023zw3x8 \
  --organization=673208786098  \
  --logtostderr=1 -v 20
```

Note, if you want to also include a map of ALL permissions<->Roles, add the flag `--includePermissions`.  
 >> This setting will take a long to complete and will significantly increase the size of the graph.


>>  NOTE: this utility will only sync ACTIVE projects

The parameters will iterate through all the gsuites user,groups as well as the projects and IAM memberships.

If you want to see more details, you can use log level `4` as shown here:

```
 go run main.go --logtostderr=1 -v 4
```

(full `groovy` text output to stdout, use level `10`)

If you want to iterate only a subcomponent, use the `--component` flag.   For example, if you just want to iterate users, run

```
 go run main.go --logtostderr=1 -v 4 --component users
```


The output of this run will generate several raw groovy files:

- `users.groovy`:  users to add to the map
- `groups.groovy`:  groups and group members to add
- `projects.groovy`:  list of the projects to add to the graph
- `roles.groovy`:  Roles and Permissions
- `serviceaccounts.groovy`:  list of the service ac
- `iam.groovy`:  IAM policy maps.


Note, `init.groovy` generates the index, schema, properties incase you need to define them.  At the moment the config defines a no-op property

```groovy
mgmt = graph.openManagement()
p = mgmt.getPropertyKey("noop")
if (p == null) {
    mgmt.makePropertyKey('noop').dataType(String.class).cardinality(org.janusgraph.core.Cardinality.LIST).make()
    mgmt.commit()
}
p = mgmt.getPropertyKey('noop')
graph.tx().rollback()

if (m.getGraphIndex('byNoopComposite') == false) {
   mgmt = graph.openManagement()
   mgmt.buildIndex('byNoopComposite', Vertex.class).addKey(p).buildCompositeIndex()
   mgmt.commit()
   // ManagementSystem.awaitGraphIndexStatus(graph, 'byNoopComposite').call()
}
```

Use the init.groovy file to define properties, verticies, constraints and index values

- [JanusGraph Schema](https://docs.janusgraph.org/basics/schema/)
- [JanusGraph Index](https://docs.janusgraph.org/index-management/index-performance/#graph-index)

Combine all the files:

```bash
cat init.groovy users.groovy serviceaccounts.groovy groups.groovy projects.groovy iam.groovy roles.groovy > all.groovy
```

Then make sure Janusgraph and gremlin are both running before loading each file.

in the gremlin console, run

```
gremlin> :load  /path/to/all.groovy
```

Note, if you enabled `--includePermissions`, this load may take upto an hour+, right.  Even if not, it may take sometime (like an hour+)...you'll see progress though...get a coffee.

if its all configured, you should see an output displaying the vertices and edges that were created.  (see section below about visualizing the graph)


## References

- [https://docs.janusgraph.org/latest/getting-started.html](https://docs.janusgraph.org/latest/getting-started.html)
- [gremlin-server](http://tinkerpop.apache.org/docs/current/reference/#gremlin-server)
- [https://github.com/bricaud/graphexp](https://github.com/bricaud/graphexp)
- [https://medium.com/@BGuigal/janusgraph-python-9e8d6988c36c](https://medium.com/@BGuigal/janusgraph-python-9e8d6988c36c)
- [https://github.com/apache/tinkerpop/tree/master/gremlin-python/](https://github.com/apache/tinkerpop/tree/master/gremlin-python/)
- [https://www.compose.com/articles/graph-101-traversing-and-querying-janusgraph-using-gremlin/](https://www.compose.com/articles/graph-101-traversing-and-querying-janusgraph-using-gremlin/)

### Gremlin References

#### Drop All Vertices and Edges

- On Gremlin Console
```bash
g.V().drop()
g.E().drop()
```

For gremlin-python, simply append suffix commands to submit the request to Gremlin-Server, eg: ```.next()```, ```.iterate()```:

```bash
g.V().drop().iterate()
g.E().drop().iterate()
```

Sample query to retrieve a user and its edges:

* Outbound Edges from a Vertex:
```
gremlin> g.V().hasLabel('user').has('email', 'user1@esodemoapp2.com').outE()
==>e[1d0d-1eqw-4etx-iyw][65768-in->24584]
==>e[1an1-1eqw-4etx-1o3s][65768-in->77896]
==>e[1925-1eqw-4etx-1o88][65768-in->78056]
==>e[1btp-1eqw-4etx-oej80][65768-in->40988880]
```

* Connected Vertices from a Vertex:
```
gremlin> g.V().hasLabel('user').has('email', 'user1@esodemoapp2.com').out().valueMap()
==>{gid=[subgroup1@esodemoapp2.com], isExternal=[false]}
==>{gid=[group1_3@esodemoapp2.com], isExternal=[false]}
==>{gid=[all_users_group@esodemoapp2.com], isExternal=[false]}
==>{gid=[group_external_mixed1@esodemoapp2.com], isExternal=[false]}
```

### Visualizing the Graph

There are several ways to visualize the generated graph:


#### Neo4J and OrientDB

I havne't tried it but you should be able to export the graph to `GraphML` and then import into Neo4J.   See:

- [Neo4J GraphML](https://neo4j.com/labs/apoc/4.1/import/graphml/)
- [OrientDB GraphML](https://orientdb.com/docs/2.2.x/Import-from-Neo4j-using-GraphML.html)

To export the graph to graphML, see the section below about CytoScape

There are probably other ways to export from janusgrahph/gremlin and import...

#### Cytoscape

- Export graph to GraphML file:

```
gremlin> sg = g.V().outE().subgraph('sg').cap('sg').next()
==>tinkergraph[vertices:183 edges:290]
```

If you exported the role<->permission (eg, used export `--includePermissions`), then the graph is much, much larger

```
gremlin> sg = g.V().outE().subgraph('sg').cap('sg').next()
==>tinkergraph[vertices:4755 edges:33834]
```

Finally export the graph:

```
gremlin> sg.io(IoCore.graphml()).writeGraph("/tmp/mygraph.xml")
==>null
```

- Import GraphML to Cytoscape

on Cytoscape, ```File->Import->Network->File```,  Select ```GraphMLFile```  the ```/tmp/mygraph.xml```

Upon import you should see the Cytosscape rendering:

The graph below uses the defaults included in this repo which covers: users, groups, serviceAccounts, all roles, projects, GCS buckets

- ![images/cytoscape.png](images/cytoscape.png)

roles that are in use

- ![images/cytoscape_roles.png](images/cytoscape_roles.png)

However, if you used, ``--includePermissions` then the graph starts to look like the corona virus

- ![images/cytoscape_permissions.png](images/cytoscape_permissions.png)

If you zoom in, you can see some details after you enable the appropriate filters

- ![images/cytoscape_role_permission_details.png](images/cytoscape_role_permission_details.png)

(i don't know how to use cytoscape at all so thats the limit of my usability: zoom/unzoom with labels)
#### graphexp

```
git clone https://github.com/bricaud/graphexp.git

cd graphexp
firefox index.html
```

- ![images/graphexp.png](images/graphexp.png)

I highly doubt you can render any graph that uses `--includePermissions` using graphep...

### Gephi

Export to Gephi for Streaming

```bash
gremlin> :remote connect tinkerpop.gephi
==>Connection to Gephi - http://localhost:8080/workspace1 with stepDelay:1000, startRGBColor:[0.0, 1.0, 0.5], colorToFade:g, colorFadeRate:0.7, startSize:10.0,sizeDecrementRate:0.33

gremlin> :remote list
==>0 - Gremlin Server - [localhost/127.0.0.1:8182]-[1f4452c0-4580-4ecf-9648-bc668c4ee68e]
==>*1 - Gephi - [workspace1]
```

#### Permissions as Role Properties

One option that is not implemented in this branch (but is in earlier commits), is to attach the permissions to roles as properties.

For example
```groovy
		if (g.V().hasLabel('role').has('name', 'roles/appengine.appViewer').has('projectid', 'netapp-producer').hasNext()  == false) {

			v = graph.addVertex('role')
			v.property('name', 'roles/appengine.appViewer')
			v.property('projectid', 'netapp-producer')

			 v.property('permissions', 'appengine.applications.get'); v.property('permissions', 'appengine.instances.get'); v.property('permissions', 'appengine.instances.list'); v.property('permissions', 'appengine.operations.get'); v.property('permissions', 'appengine.operations.list'); v.property('permissions', 'appengine.ser
vices.get'); v.property('permissions', 'appengine.services.list'); v.property('permissions', 'appengine.versions.get'); v.property('permissions', 'appengine.versions.list'); v.property('permissions', 'resourcemanager.projects.get'); v.property('permissions', 'resourcemanager.projects.list');
		}
```

We can do that since we defined the `permissions` property as a `LIST` in `init.groovy`:

```groovy
mgmt = graph.openManagement()
p = mgmt.getPropertyKey("permissions")
if (p == null) {
    mgmt.makePropertyKey('permissions').dataType(String.class).cardinality(org.janusgraph.core.Cardinality.LIST).make()
    mgmt.commit()
}
```

However, i ran into some issues issues:

1. The current application writes the groovy files to disk and imports it via the admin gremlin CLI.
   This ofcourse isn't the right way to do this but i don't know this tech very well.
   Loading a vary large permission set (which can number 1000s for things like `role/owner`) will cause a socket timeout in gremlin cli.
   This is certainly a solvable problem but i haven't invested the time into this feature

2. GraphML export format does not suport multi-valued properties.
   Well...thats as far as i know...if you try to export the graph, you'll see

   ```groovy
   gremlin> sg.io(IoCore.graphml()).writeGraph("/tmp/mygraph.xml")
        Multiple properties exist for the provided key, use Vertex.properties(permissions
   ```

   I do know you can GraphSON format does support it but i don't know of a utility that will render it

```groovy
    mapper = GraphSONMapper.build().addCustomModule(org.janusgraph.graphdb.tinkerpop.io.graphson.JanusGraphSONModuleV2d0.getInstance()).create()
    writer = GraphSONWriter.build().mapper(mapper).create()
    file = new FileOutputStream("/tmp/mygraph.json")
    writer.writeGraph(file, sg)
```


So instead, i just made the permissions as their own nodes.