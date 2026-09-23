# Claude Code lease check hook

1. Copy `settings-snippet.json` into your `.claude/settings.json`:
   ```json
   {
     "preToolUse": {
       "matcher": { "type": "any", "matchers": [
         {"type": "tool", "name": "Edit"},
         {"type": "tool", "name": "Write"},
         {"type": "tool", "name": "Bash"}
       ]},
       "command": "${PROJECT_DIR}/contrib/hooks/claude-code/check-lease.sh",
       "mode": "warn"
     }
   }
   ```
2. **Optional**: Export `DIBS_ACTOR=<your-agent-name>` (unique per concurrent instance, e.g. session-PID suffix: `claude-code-$$`). If omitted, `dibs` will automatically infer the agent identity from the process tree.
3. Set `DIBS_HOOK_MODE=block` to block instead of warn.
