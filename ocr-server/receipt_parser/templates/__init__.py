"""Bank templates. Import every template module here so it registers itself.

To add a bank: create ``<bank>.py`` next to ``bca.py`` and add an import below.
"""
from . import bca  # noqa: F401
from . import bni  # noqa: F401
