# Capacity Codex credential recovery

Capacity remains the selected subscription owner. The gateway reads an account's
persisted access token and identity in place; it neither reads refresh tokens nor
writes the official client's auth.json. The former independent refresh/write path
has been removed.

For expiry or an upstream 401, CommandRefresher asks the authenticated loopback
Capacity owner to retry that exact account. Native hosts use /native/request;
service hosts use the public operation directly. A terminal reauthentication state
cannot be bypassed by gateway traffic. The bridge then verifies the same source
is live, and the credential reloads and verifies the durable file. An unchanged
rejected token or an expired token cannot pass. The credential cooldown is one
minute; a transient failed refresh no longer disables an unchanged file forever.

The official Codex app-server performs renewal in the provider reader. Hopper's
profile-local kernel lock serializes quota/history and other client sessions across
processes. The gateway has no second credential writer to race it.

Validation covers owner delegation, unchanged/expired tokens, persistence failure,
transient recovery, identity mismatch, terminal refusal and the native operation
envelope. Runtime cutover is separate from source publication. Existing gateway
processes require the owner-managed executable update before this behavior is live.

Official managed authentication: https://learn.chatgpt.com/docs/app-server
