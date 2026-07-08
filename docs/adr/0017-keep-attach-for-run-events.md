# Keep Attach For Run Events

Maya Stall will use `attach` in the Crabbox sense: following run or session events and logs. Reusing a Maya UI Session for execution remains Debug Attach, and opening a UI viewer should use a separate future command so `attach` does not mean both log streaming and desktop viewing. Run-scoped `attach <run-id> screenshot` and `attach <run-id> control click` are bounded operator actions for an active or kept run that already owns the Host Lock; they must verify run ownership before touching the desktop.
