#!/bin/sh
# Run locally in an interactive terminal. Never send the private key or its
# passphrase to the server or put it in a chat. Creates a new directory only.
set -eu
umask 077
if [ "$#" -ne 1 ]; then
    printf 'Usage: %s /path/to/new-private-key-directory\n' "$0" >&2
    exit 2
fi
key_dir=$1
if ! mkdir -m 700 -- "$key_dir"; then
    printf 'Directory already exists or could not be created; nothing was overwritten.\n' >&2
    exit 1
fi
set -C
openssl genpkey -algorithm RSA -aes-256-cbc -pkeyopt rsa_keygen_bits:3072 > "$key_dir/private.pem"
openssl pkey -in "$key_dir/private.pem" -pubout > "$key_dir/public.pem"
printf 'Created encrypted private key and public key. Store the private key and passphrase separately from backups.\n'
printf 'Only public.pem should be copied to the server.\n'
