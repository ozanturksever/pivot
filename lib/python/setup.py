from __future__ import absolute_import
from setuptools import setup, find_packages


setup(
    name='pivot-client',
    description='Client library for integrating with the Pivot database abstraction service.',
    version='0.1.2',
    author='Gary Hetzel',
    author_email='garyhetzel+pivot@gmail.com',
    url='https://github.com/ozanturksever/pivot',
    install_requires=[
        'requests',
        'six',
    ],
    packages=find_packages(exclude=['*.tests']),
    classifiers=[],
)
