#!/bin/bash

FILENAME=$1
TITLE=$2
OUTPUT=./docs/${FILENAME}.md

echo "# ${TITLE}" >${OUTPUT}
echo "Test item | Case description | File location" >>${OUTPUT}
echo "---|---|---" >>${OUTPUT}
CSV=$(mktemp)
jq -r '.[]|select(.SpecReports!=null)|.SpecReports[]|select(.ContainerHierarchyTexts!=null) | [(.ContainerHierarchyTexts | join("/")), .LeafNodeText, .LeafNodeLocation.FileName] | @csv ' <${FILENAME}.json | sort >${CSV}
sed 's:'$(pwd)'::g' ${CSV} | sed 's/","/|/g; s/"//g' >>${OUTPUT}
