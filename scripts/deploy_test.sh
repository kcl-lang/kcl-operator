#!/bin/bash

make deploy
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

echo -e "${YELLOW}Monitoring deployment rollout status...${NC}"
kubectl rollout status deploy/kcl-webhook-server --timeout=1m
ROLLOUT_STATUS=$?

if [ $ROLLOUT_STATUS -ne 0 ]; then
    echo -e "${RED}Deployment rollout failed.${NC}"

    echo -e "${YELLOW}Retrieving deployment details...${NC}"
    kubectl describe deploy/kcl-webhook-server

    echo -e "${YELLOW}Retrieving ReplicaSet details...${NC}"
    RS=$(kubectl get rs -l=app=kcl-webhook-server --output=jsonpath='{.items[*].metadata.name}')
    for rs in $RS; do
        echo -e "${YELLOW}ReplicaSet: $rs${NC}"
        kubectl describe rs/$rs
    done

    echo -e "${YELLOW}Retrieving Pod details...${NC}"
    PODS=$(kubectl get pods -l=app=kcl-webhook-server --output=jsonpath='{.items[*].metadata.name}')
    for pod in $PODS; do
        echo -e "${YELLOW}Pod: $pod${NC}"
        kubectl describe pod/$pod
        echo -e "${YELLOW}Pod init container logs:${NC}"
        kubectl logs $pod -c kcl-webhook-init
        echo -e "${YELLOW}Pod main container logs:${NC}"
        kubectl logs $pod -c kcl-webhook-server
    done
else
    echo -e "${GREEN}Deployment rollout successful.${NC}"
    echo -e "${YELLOW}Deploying the KCL source...${NC}"
    kubectl apply -f- << EOF
apiVersion: krm.kcl.dev/v1alpha1
kind: KCLRun
metadata:
  name: set-annotation
spec:
  params:
    annotations:
      managed-by: kcl-operator
  source: |
    items = [item | {
        metadata.annotations: {"managed-by" = "kcl-operator"}
    } for item in option("items")]
EOF
    echo -e "${YELLOW}Validating the mutation result by creating a nginx Pod YAML...${NC}"
    kubectl apply -f- << EOF
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  annotations:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx:1.14.2
    ports:
    - containerPort: 80
EOF
    echo -e "${YELLOW}Checking if the annotation 'managed-by: kcl-operator' is added to the pod...${NC}"
    MANAGED_BY=$(kubectl get po nginx -o yaml | grep kcl-operator)
    if [ -n "$MANAGED_BY" ]; then
        echo -e "${GREEN}The annotation 'managed-by: kcl-operator' is added to the pod.${NC}"
    else
        echo -e "${RED}The annotation 'managed-by: kcl-operator' is not added to the pod.${NC}"
    fi
fi
