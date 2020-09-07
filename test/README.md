This directory has test commands with interesting dependency trees.

Intreseting dependency trees are, for example:

-   mutually depending Go modules (but no package cycles -- which is valid):

```
test/mod1/cmd/hello -> test/mod2/pkg/exthello
test/mod2/pkg/exthello -> test/mod1/pkg/hello
test/mod2/pkg/exthello -> test/mod3/pkg/hello
```

-   two different modules depending on one third-party module at different
    versions
