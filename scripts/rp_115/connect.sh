#!/bin/bash

# SImulate user login for the foritnet portal (ethernet iitgn)

read -p "Identity: " id
read -s -p "Password: " pw
echo

u=$(curl -s http://example.com | sed -n 's/.*window.location="\([^"]*\)".*/\1/p')
if [ -z "$u" ]; then
    echo "Could not find redirect URL; treating as success."
    exit 0
fi
curl -ks -c /tmp/portal_cookies.txt --http1.1 -A 'Mozilla/5.0' "$u" -o /tmp/portal.html
redir=$(sed -n 's/.*name="4Tredir" value="\([^"]*\)".*/\1/p' /tmp/portal.html)
magic=$(sed -n 's/.*name="magic" value="\([^"]*\)".*/\1/p' /tmp/portal.html)


curl -ks  -L \
        -b /tmp/portal_cookies.txt \
        -c /tmp/portal_cookies.txt \
        -A 'Mozilla/5.0' \
        -d "4Tredir=$redir" \
        -d "magic=$magic" \
        --data-urlencode "username=$id" \
        --data-urlencode "password=$pw" \
        -o /dev/null \
        https://fwg.iitgn.ac.in/