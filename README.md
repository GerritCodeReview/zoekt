
    "Zoekt, en gij zult spinazie eten" - Jan Eertink

    ("seek, and ye shall eat spinach" - My primary school teacher)

This is a fast text search engine, intended for use with source
code. (Pronunciation: roughly as you would pronounce "zooked" in English)

INSTRUCTIONS
============

Indexing:

    go install github.com/google/zoekt/cmd/zoekt-index
    $GOPATH/bin/zoekt-index .

Searching

    go install github.com/google/zoekt/cmd/zoekt
    $GOPATH/bin/zoekt 'ngram f:READ'

Indexing git repositories:

    go install github.com/google/zoekt/cmd/zoekt-git-index
    $GOPATH/bin/zoekt-git-index -branches master,stable-1.4 -prefix origin/ .

Starting the web interface

    go install github.com/google/zoekt/cmd/zoekt-webserver
    $GOPATH/bin/zoekt-webserver -listen :6070


SEARCH SERVICE
==============

Zoekt comes with a small service management program:

    go install github.com/google/zoekt/cmd/zoekt-server

    cat << EOF > config.json
    [{"GithubUser": "username"},
     {"GitilesURL": "https://gerrit.googlesource.com", Name: "zoekt" }
    ]
    EOF

    $GOPATH/bin/zoekt-server -mirror_config config.json

This will mirror all repos under 'github.com/username' as well as the
'zoekt' repository. It will index the repositories and start the webserver interface.

It takes care of fetching and indexing new data, restarting crashed
webservers and cleaning up logfiles


SYMBOL SEARCH
=============

It is recommended to install exuberant or universal CTags. If
available, it will be used to improve ranking.

If you index untrusted code, it is strongly recommended to also
install Bazel's sandbox, to avoid vulnerabilities of ctags opening up
access to the indexing machine. The sandbox can be compiled as follows:

    for f in namespace-sandbox.c namespace-sandbox.c process-tools.c network-tools.c \
       process-tools.h network-tools.h ; do \
      wget https://raw.githubusercontent.com/bazelbuild/bazel/master/src/main/tools/$f \
    done
    gcc -o namespace-sandbox -std=c99 \
       namespace-sandbox.c process-tools.c network-tools.c  -lm
    cp namespace-sandbox /usr/local/bin/


BACKGROUND
==========

This uses ngrams (n=3) for searching data, and builds an index containing the
offset of each ngram's occurrence within a file.  If we look for "the quick
brown fox", we look for two trigrams (eg. "the" and "fox"), and check that they
are found at the right distance apart.

Regular expressions are handled by extracting normal strings from the regular
expressions. For example, to search for

  (Path|PathFragment).*=.*/usr/local

we look for

  (AND (OR substr:"Path" substr:"PathFragment") substr:"/usr/local")

and any documents thus found would be searched for the regular
expression.

Compared to indexing 3-grams on a per-file basis, as described
[here](https://swtch.com/~rsc/regexp/regexp4.html), there are some advantages:

* for each substring, we only have to intersect just a couple of posting-lists:
  one for the beginning, and one for the end.

* we can select any pair of trigrams from the pattern for which the
  number of matches is minimal. For example, we could search for "qui"
  rather than "the".

There are some downsides compared to trigrams:

* The index is large. Empirically, it is about 3x the corpus size, composed of
  2x (offsets), and 1x (original content). However, since we have to look at
  just a limited number of ngrams, we don't have to keep the index in memory.

Compared to [suffix
arrays](https://blog.nelhage.com/2015/02/regular-expression-search-with-suffix-arrays/),
there are the following advantages:

* The index construction is straightforward, and can easily be made
  incremental.

* It uses a less memory.

* All the matches are returned in document order. This makes it
  straightforward to process compound boolean queries with AND and OR.

Downsides compared to suffix array:

* there is no way to transform regular expressions into index ranges into
  the suffix array.



ACKNOWLEDGEMENTS
================

Thanks to Alexander Neubeck for coming up with this idea, and helping me flesh
it out.


DISCLAIMER
==========

This is not an official Google product
