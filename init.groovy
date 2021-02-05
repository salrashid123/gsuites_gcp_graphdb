mgmt = graph.openManagement()
p = mgmt.getPropertyKey("permissions")
if (p == null) {
    mgmt.makePropertyKey('permissions').dataType(String.class).cardinality(org.janusgraph.core.Cardinality.LIST).make()
    mgmt.commit()
}
