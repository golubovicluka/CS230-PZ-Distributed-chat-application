allow_k8s_contexts('kind-chat')
default_registry('localhost:5001')

namespace = 'chat'

k8s_yaml(blob('''
apiVersion: v1
kind: Namespace
metadata:
  name: %s
''' % namespace))

docker_build(
    'chat-chatserver',
    '.',
    dockerfile='build/Dockerfile',
    target='chatserver',
    only=['go.mod', 'go.sum', 'cmd', 'internal'],
)

docker_build(
    'chat-web',
    'chat-app',
    dockerfile='chat-app/Dockerfile',
    ignore=['node_modules', 'dist'],
)

k8s_yaml(helm(
    'deploy/helm/chat',
    name='chat',
    namespace=namespace,
    values=['deploy/helm/chat/values-dev.yaml'],
))

k8s_resource(
    new_name='database',
    objects=['chat-db:cluster'],
    labels=['data'],
)

k8s_resource(
    new_name='secrets',
    objects=['chat-redis:externalsecret', 'chat:serviceaccount'],
    labels=['data'],
)

local_resource(
    'database-ready',
    cmd='kubectl --context kind-chat -n %s wait cluster/chat-db --for=condition=Ready --timeout=300s' % namespace,
    resource_deps=['database'],
    labels=['data'],
)

local_resource(
    'secrets-ready',
    cmd='kubectl --context kind-chat -n %s wait externalsecret/chat-redis --for=condition=Ready --timeout=120s' % namespace,
    resource_deps=['secrets'],
    labels=['data'],
)

k8s_resource('redis', resource_deps=['secrets-ready'], labels=['data'])

k8s_resource(
    'chatserver',
    objects=['chatserver:poddisruptionbudget', 'chatserver:scaledobject'],
    resource_deps=['redis', 'database-ready'],
    links=[link('https://chat.localhost', 'chat')],
    labels=['app'],
)

k8s_resource(
    'web',
    objects=['web:poddisruptionbudget', 'chat:httproute', 'chat:backendtrafficpolicy'],
    labels=['app'],
)

k8s_resource(
    new_name='observability',
    objects=[
        'chat-db:podmonitor',
        'chat-health:probe',
        'chat-alerts:prometheusrule',
        'chat-http-availability:servicelevelobjective',
        'chat-fanout-latency:servicelevelobjective',
        'chat-dashboard:configmap',
    ],
    labels=['observability'],
)
