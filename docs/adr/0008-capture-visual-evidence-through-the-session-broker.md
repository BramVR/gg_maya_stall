# Capture Visual Evidence Through The Session Broker

Maya Stall will trigger Visual Evidence capture through the Session Broker rather than trying to capture the visible desktop directly from a raw SSH session. The broker may use Crabbox-style Windows helpers such as interactive scheduled tasks internally, but the public boundary stays broker-owned so screenshots and recordings come from the same desktop session as Maya.
