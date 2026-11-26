"""The environment variables the dom0 tool reads."""

# BUNDLE is the directory holding the template packages the tool installs;
# the RPM puts them in /usr/share/torii, and a checkout run in dom0 points it
# at build/bundle (HACKING.md).
BUNDLE = "TORII_BUNDLE"
