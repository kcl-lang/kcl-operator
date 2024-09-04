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
        echo -e "${YELLOW}Pod logs:${NC}"
        kubectl logs $pod
    done
else
    echo -e "${GREEN}Deployment rollout successful.${NC}"
fi
