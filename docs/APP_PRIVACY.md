# AI Agent World app — Privacy Policy

Last updated: 27 November 2026

AI Agent World (the iPhone and Android app, bundle `com.joinanny.aiagent`) is a
remote control for an **aiagent server that you run yourself**. This page says
what the app handles and where it goes.

## The short version

- The app has **no account with us** and sends **nothing to us**: no analytics,
  no tracking, no advertising, no crash reports.
- Your conversations, pictures and voice notes go **only to your own server**.
- Notifications pass through a small relay that sees a session's name and never
  your token or your conversation.

## What the app stores on your phone

- **Connect codes (server address + access token).** Kept in the phone's secure
  storage (iOS Keychain, readable only on this device, not synced to iCloud;
  Android Keystore-backed storage, excluded from backups). Removing a profile
  deletes its token.
- **Your plan** (free or multi-profile) and push registration handles, in the
  same secure storage.

## What goes to your own server

Everything you do in the app is a request to the server in the connect code,
over HTTPS, authenticated by its token: reading what waits for you, your
messages to agents, your review decisions and notes, and pictures you attach
(uploaded to that server so its agents can read them). Your server's operator
(normally you) controls that data; see the aiagent server's own
[security notes](SECURITY.md).

## Notifications

To tell you when an agent needs you or finishes, the app registers with the
push relay `aiagent-push.hotkhwan.workers.dev` (Cloudflare Workers) and with
Google Firebase Cloud Messaging, which delivers through Apple Push Notification
service or Google. The relay stores, for each registration: the device's push
token, the platform, and the server address. Your server then sends the relay,
per notification: the registration handle, the kind (answer / instructions /
review / finished), the session or page **name**, and its id. **No access token
and no conversation text** is sent. Notifications can be turned off in the
phone's settings; removing a profile withdraws its registration.

## Voice and pictures

- **Voice:** the microphone button uses the phone's speech recognition
  (Apple Speech on iOS, Google on Android), which may process audio on the
  provider's servers. The app keeps no recording; only the resulting text is
  sent, and only to your server, when you press Send.
- **Camera and photos:** used only when you choose to attach a picture.

## Purchases

The multi-profile subscription is sold by Apple (or Google). We do not receive
your payment details.

## Children

The app is a developer tool, not directed at children.

## Contact

Questions or requests: open an issue at
<https://github.com/hotkhwan/aiagent/issues>.
