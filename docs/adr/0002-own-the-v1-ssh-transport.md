# Own The V1 SSH Transport

Maya Stall v1 will implement its own minimal SSH, sync, and artifact collection path instead of requiring the Crabbox binary at runtime. Crabbox remains a reference for config shape, static SSH behavior, desktop evidence, and artifact discipline, but a direct Maya-specific transport keeps owned Maya Hosts simpler to prepare and lets Maya logs, scenes, screenshots, and session broker behavior be first-class.
