#!/usr/bin/python

from apiclient.discovery import build
import httplib2
from oauth2client.service_account import ServiceAccountCredentials
from oauth2client.client import GoogleCredentials
import logging
import json
import sys

from apiclient import discovery
import oauth2client
from oauth2client import client
from oauth2client import tools

from gremlin_python import statics
from gremlin_python.structure.graph import Graph
from gremlin_python.process.graph_traversal import __
from gremlin_python.process.strategies import *
from gremlin_python.driver.driver_remote_connection import DriverRemoteConnection
from gremlin_python.process.traversal import T, P, Operator

from gremlin_python.process.graph_traversal import GraphTraversalSource

statics.load_statics(globals())
graph = Graph()
g = graph.traversal().withRemote(DriverRemoteConnection('ws://localhost:8182/gremlin','g'))

g.V().drop().iterate()
g.E().drop().iterate()

domain_id='C023zw3x8'
domain_name = 'esodemoapp2.com'

# for JSON_CERTIFICATE_FILES
#os.environ["GOOGLE_APPLICATION_CREDENTIALS"] = "service+account.json"
#credentials = GoogleCredentials.get_application_default()

gsuites_credentials = ServiceAccountCredentials.from_json_keyfile_name('service_account.json')
gsuites_scopes = ['https://www.googleapis.com/auth/admin.directory.user.readonly', 'https://www.googleapis.com/auth/admin.directory.group.readonly']

if gsuites_credentials.create_scoped_required():
  gsuites_credentials = gsuites_credentials.create_scoped(gsuites_scopes)
gsuites_credentials = gsuites_credentials.create_delegated('admin@esodemoapp2.com')

gsuites_http = httplib2.Http()
gsuites_http = gsuites_credentials.authorize(gsuites_http)
gsuites_credentials.refresh(gsuites_http)

directory_service = discovery.build('admin', 'directory_v1', http=gsuites_http)
gcp_credentials = ServiceAccountCredentials.from_json_keyfile_name('service_account.json')
gcp_scopes = ['https://www.googleapis.com/auth/iam','https://www.googleapis.com/auth/cloud-platform']

if gcp_credentials.create_scoped_required():
  gcp_credentials = gcp_credentials.create_scoped(gcp_scopes)

gcp_http = httplib2.Http()
gcp_http = gcp_credentials.authorize(gcp_http)
gcp_credentials.refresh(gcp_http)

iam_service = build(serviceName='iam', version= 'v1',http=gcp_http)
crm_service = build(serviceName='cloudresourcemanager', version= 'v1',http=gcp_http)


def getGroupMembers(groupKey, service):
  print '======================= Group Members for group: ' + groupKey
  request = ''
  while request is not None:
    includeDerivedMembership = True
    results = directory_service.members().list(groupKey=groupKey).execute()
    for m in results['members']:
      groupType = m['type']
      if (groupType == 'CUSTOMER'):
        print "     GroupID: " + m['id'] + " Type: " + m['type']
        g1 = g.V().hasLabel('group').has('gid', groupKey).next()
        print "     Adding " + 'ALL Users ' + " --memberOf--> " + groupKey
        e1 = g.V().addE('memberOf').to(g1).property('weight', 1).next()
      elif (groupType =='USER'):
        #print "     Member Email: " + m['email'] +  " Type: " + m['type']
        if ( len( g.V().hasLabel('user').has('uid', m['email']).toList() ) == 0):
          g.addV('user').property(label, 'user').property('uid', m['email'] ).property('isExternal', True).next()
        u1 = g.V().hasLabel('user').has('uid', m['email'] ).next()
        g1 = g.V().hasLabel('group').has('gid', groupKey).next()
        try:
          g.V(u1).outE('memberOf').where(inV().hasId(g1.id)).next()
        except StopIteration:
          print "     Adding " + m['email'] + " --memberOf--> " + groupKey
          e1 = g.V(u1).addE('memberOf').to(g1).property('weight', 1).next()

      elif (groupType =='GROUP'):
        print "     GroupID: " + m['id'] + " GroupEmail: " + m['email'] + " Type: " + m['type']
        g1 = g.V().hasLabel('group').has('gid', m['email'] ).next()
        g2 = g.V().hasLabel('group').has('gid', groupKey).next()
        print "     Adding " + m['email'] + " --memberOf--> " + groupKey
        e1 = g.V(g1).addE('memberOf').to(g2).property('weight', 1).next()
        print '     >>> Recursion on '  + m['email']
        getGroupMembers(m['email'],service)
    #print json.dumps(results, sort_keys=True, indent=4)
    request = service.members().list_next(request, results)


def getGroups():
  print '======================= Groups '
  request = ''
  while request is not None:
    results = directory_service.groups().list(customer=domain_id, domain=domain_name).execute()
    groups = results.get('groups', [])
    #print json.dumps(results, sort_keys=True, indent=4)
    for gr in groups:
      group_email = gr['email']
      print "  " + group_email
      gid = gr['id']
      g.addV('group').property(label, 'group').property('gid', group_email).property('isExternal', False).id().next()
    request = directory_service.groups().list_next(request, results)

  request = ''
  while request is not None:
    results = directory_service.groups().list(customer=domain_id, domain=domain_name).execute()
    groups = results.get('groups', [])
    for gr in groups:
      group_email = gr['email']
      gid = gr['id']
      getGroupMembers(group_email,directory_service)
    request = directory_service.groups().list_next(request, results)

def getUsers():
  print '======================= Users '
  request = ''
  while request is not None:
    results = directory_service.users().list(customer=domain_id, domain=domain_name).execute()
    users = results.get('users', [])
    #print json.dumps(users, sort_keys=True, indent=4)
    for u in users:
      email =  u['primaryEmail']
      print '  ' + email
      uid = u['id']
      customerId = u['customerId']
      g.addV('user').property(label, 'user').property('uid', email).property('isExternal', False).id().next()
    request = directory_service.users().list_next(request, results)

def getServiceAccounts(project_id):
  print '======================= ServiceAccounts for project ' + project_id
  request = ''
  while request is not None:
    results = iam_service.projects().serviceAccounts().list(name='projects/' + project_id).execute()
    #print json.dumps(results, sort_keys=True, indent=4)
    serviceAccounts = results['accounts']
    for a in serviceAccounts:
      uniqueId = a['uniqueId']
      email = a['email']
      displayName = a['displayName']
      if ( len(  g.V().hasLabel('user').has('uid', email).toList() ) == 0 ):
        print "     Adding ServiceAccount " + email
        g.addV('user').property(label, 'user').property('uid', email).property('isExternal', False).id().next()

    request = iam_service.projects().serviceAccounts().list_next(request, results)

def getIamPolicy(project_id):
  print '======================= Iam Policy for project ' + project_id
  results = crm_service.projects().getIamPolicy(resource=project_id).execute()
  #print json.dumps(results, sort_keys=True, indent=4)
  try:
    bindings = results['bindings']
    for binding in bindings:
      role = binding['role']
      if ( len ( g.V().hasLabel('role').has('rolename', role).toList() ) == 0 ):
        g.addV('role').property(label, 'role').property('rolename', role).id().next()

      p1 = g.V().hasLabel('project').has('projectId', project_id).next()
      r1 = g.V().hasLabel('role').has('rolename', role).next()
      print "     Adding " + project_id + " --hasRole--> " + role
      e1 = g.V(p1).addE('hasRole').to(r1).property('weight', 1).next()

      members = binding['members']
      for member in members:
        member_type = member.split(':')[0]
        email = member.split(':')[1]

        r1 = g.V().hasLabel('role').has('rolename', role).next()

        if (member_type == 'user'):
          if ( len( g.V().hasLabel('user').has('uid', email).toList() ) == 0 ):
            g.addV('user').property(label, 'user').property('uid', email).property('isExternal', True).id().next()
          i1 = g.V().hasLabel('user').has('uid', email).next()

        if (member_type == 'serviceAccount'):
          if ( len( g.V().hasLabel('user').has('uid', email).toList() ) == 0 ):
            g.addV('user').property(label, 'user').property('uid', email).property('isExternal', True).id().next()
          i1 = g.V().hasLabel('user').has('uid', email).next()


        if (member_type == 'group'):
          if ( len( g.V().hasLabel('group').has('gid', email).toList() ) == 0 ):
            g.addV('group').property(label, 'group').property('gid', email).property('isExternal', True).id().next()
          i1 = g.V().hasLabel('group').has('gid', email).next()

        try:
          g.V(r1).outE('hasMember').where(inV().hasId(i1.id)).next()
        except StopIteration:
          print "     Adding " + role + " --hasMember--> " + email
          e1 = g.V(r1).addE('hasMember').to(i1).property('weight', 1).next()

  except KeyError:
    pass

def getCustomRoles(project_id):
  print '======================= CustomRoles for project ' + project_id
  request = ''
  while request is not None:
    results = iam_service.projects().roles().list(parent='projects/' + project_id).execute()
    #print json.dumps(results, sort_keys=True, indent=4)
    try:
      roles = results['roles']
      for r in roles:
        name = r['name']
        print '     ' + name
        title = r['title']
        if ( len( g.V().hasLabel('role').has('rolename', name).toList() ) == 0 ):
          g.addV('role').property(label, 'role').property('rolename', name).id().next()
    except KeyError:
      pass
    request = iam_service.projects().roles().list_next(request, results)

def getProjects():
  print '======================= Get Projects '
  request = ''
  while request is not None:
    results = crm_service.projects().list().execute()
    #print json.dumps(results, sort_keys=True, indent=4)
    projects = results['projects']
    for p in projects:
      projectNumber = p['projectNumber']
      projectId = p['projectId']
      if ( len(g.V().hasLabel('project').has('projectId', projectId).toList()) == 0 ):
        g.addV('project').property(label, 'project').property('projectId', projectId).id().next()
      getServiceAccounts(projectId)
      getCustomRoles(projectId)
      getIamPolicy(projectId)
    request = crm_service.projects().list_next(request, results)

getUsers()
getGroups()
getProjects()
