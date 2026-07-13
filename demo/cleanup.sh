#!/bin/bash
# Clean up demo session files and sentinel files
rm -rf ~/.claude/projects/-Users-demo-*/
rm -f ~/.claude/projects/-Users-demo-*/.loopguard-stop
echo "Demo session files cleaned up."
