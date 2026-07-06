# Capture Visual Evidence Through The Session Broker

Maya Stall will trigger Visual Evidence capture through the Session Broker rather than trying to capture the visible desktop directly from SSH. The broker may use Crabbox-style Windows helpers such as interactive scheduled tasks internally, but the public boundary stays broker-owned so screenshots come from the same desktop session as Maya. Recording remains deferred until the Session Broker exposes real recording capture.
