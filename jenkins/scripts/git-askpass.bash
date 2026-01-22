#!/bin/bash
case "$1" in
Username*) exec printf "%s" "${GH_CREDENTIALS_USR}" ;;
Password*) exec printf "%s" "${GH_CREDENTIALS_PSW}" ;;
esac
