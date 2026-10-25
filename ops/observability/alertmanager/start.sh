#!/bin/sh
# Renders the Alertmanager config from the environment and starts it. The
# SMTP password goes to a file inside the container only.
set -eu
: "${ALERT_EMAIL_TO:=yuezhangwork812@gmail.com}"
: "${ALERT_SMTP_HOST:=smtp.gmail.com:587}"
: "${ALERT_SMTP_USER:=}"
: "${ALERT_SMTP_FROM:=${ALERT_SMTP_USER:-$ALERT_EMAIL_TO}}"
if [ -z "$ALERT_SMTP_USER" ] || [ -z "${ALERT_SMTP_PASSWORD:-}" ]; then
  echo "alertmanager: ALERT_SMTP_USER / ALERT_SMTP_PASSWORD not set in .env; alerts are collected but no email is sent" >&2
fi
umask 077
printf '%s' "${ALERT_SMTP_PASSWORD:-}" > /tmp/smtp_password
sed -e "s|\${ALERT_SMTP_HOST}|${ALERT_SMTP_HOST}|g" -e "s|\${ALERT_SMTP_FROM}|${ALERT_SMTP_FROM}|g" \
    -e "s|\${ALERT_SMTP_USER}|${ALERT_SMTP_USER}|g" -e "s|\${ALERT_EMAIL_TO}|${ALERT_EMAIL_TO}|g" \
    /etc/alertmanager/alertmanager.yml.tmpl > /tmp/alertmanager.yml
exec /bin/alertmanager --config.file=/tmp/alertmanager.yml --storage.path=/alertmanager
