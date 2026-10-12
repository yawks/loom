#!/bin/sh
# Usage : sh lignes_absentes.sh arg1.txt arg2.txt

if [ "$#" -ne 2 ]; then
    echo "Usage : $0 arg1.txt arg2.txt" >&2
    exit 2
fi

grep -F -x -v -f "$1" -- "$2"