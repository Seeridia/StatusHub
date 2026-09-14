# Brand icon sources

StatusHub stores brand identifiers, not third-party logo files. The console loads
vendor artwork at runtime using this order:

1. [Devicon](https://devicon.dev/) SVG files through the official jsDelivr URL
   pattern documented by the project.
2. [Simple Icons](https://simpleicons.org/) through its branded SVG CDN.
3. The built-in generic vendor icon when neither source has the brand or a
   request fails.

Known services use explicit icon identifiers from the upstream catalogs. For a
vendor outside StatusHub's suggestion catalog, the console derives a safe slug
from the user-entered name and tries Devicon's `original` and `plain` variants,
then the matching Simple Icons slug. Names that cannot produce an ASCII slug go
directly to the generic icon.

Remote images use lazy loading, asynchronous decoding, and a no-referrer policy.
The Content Security Policy only permits image requests to `cdn.jsdelivr.net`
and `cdn.simpleicons.org` in addition to same-origin assets and data URLs.

Vendor names and logos belong to their respective owners. Displaying a logo
identifies a service and does not imply affiliation, endorsement, or verified
adapter compatibility.
