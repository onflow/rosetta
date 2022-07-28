#! /usr/bin/env python3

import base64
import sys

print(base64.b64encode(bytes.fromhex(sys.argv[1])).decode("utf-8"))
