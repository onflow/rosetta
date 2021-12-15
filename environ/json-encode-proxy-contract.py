#! /usr/bin/env python3

import json

f = open("script/FlowColdStorageProxy.cdc", "r", encoding="utf-8")
print(json.dumps(f.read()))
