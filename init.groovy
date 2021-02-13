mgmt = graph.openManagement()
p = mgmt.getPropertyKey("noop")
if (p == null) {
    mgmt.makePropertyKey('noop').dataType(String.class).cardinality(org.janusgraph.core.Cardinality.LIST).make()
    mgmt.commit()
}
p = mgmt.getPropertyKey('noop')
graph.tx().rollback() //Never create new indexes while a transaction is active


if (m.getGraphIndex('byNoopComposite') == false) {
   mgmt = graph.openManagement()
   mgmt.buildIndex('byNoopComposite', Vertex.class).addKey(p).buildCompositeIndex()
   mgmt.commit()
   // ManagementSystem.awaitGraphIndexStatus(graph, 'byNoopComposite').call()
}
