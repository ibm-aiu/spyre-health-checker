# input.txt

This file contains sample `lspci` output to be used for unit test verification.
It is based on actual output from a cluster with multiple Spyre cards. Some
of the device entries are "synthetic" in that the original output was
modified to include data of interest such as "(rev 01)", "(rev 02)", "(rev ff)"
as well as additional vendor:device strings such as "[1014:9999]"
(sample unsupported device), and "[1014:06a8]" (VF mode Spyre).
